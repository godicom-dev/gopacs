package archive

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/godicom-dev/gopacs/internal/id"
)

// Change is one entry in the Orthanc changes log.
type Change struct {
	Seq          int64
	Type         string
	ResourceType string
	ID           string
	Date         string
}

// Stats is a global or per-resource size summary. Count flags say which
// counters Orthanc includes at that level.
type Stats struct {
	Patients, Studies, Series, Instances                 int
	ShowPatients, ShowStudies, ShowSeries, ShowInstances bool
	Disk                                                 int64
	Global                                               bool
}

// ArchiveFile is one DICOM file to place in an Orthanc archive ZIP.
type ArchiveFile struct {
	Name string
	Path string
}

type removed struct {
	instances, series, studies, patients []string
}

func notExists(tx *sql.Tx, table, id string) (bool, error) {
	var one int
	err := tx.QueryRow(`SELECT 1 FROM `+table+` WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	return false, err
}

func recordChange(tx *sql.Tx, changeType, resourceType, id, when string) error {
	_, err := tx.Exec(`INSERT INTO changes(change_type, resource_type, resource_id, created_at) VALUES(?, ?, ?, ?)`,
		changeType, resourceType, id, when)
	return err
}

func (s *Store) removedIDs(kind, canon string) (removed, error) {
	var out removed
	switch kind {
	case "instance":
		out.instances = []string{canon}
	case "series":
		ids, err := s.ids(`SELECT id FROM instances WHERE series_id = ?`, canon)
		if err != nil {
			return removed{}, err
		}
		out.instances = ids
		out.series = []string{canon}
	case "study":
		inst, err := s.ids(`SELECT i.id FROM instances i JOIN series se ON se.id = i.series_id WHERE se.study_id = ?`, canon)
		if err != nil {
			return removed{}, err
		}
		series, err := s.ids(`SELECT id FROM series WHERE study_id = ?`, canon)
		if err != nil {
			return removed{}, err
		}
		out.instances, out.series, out.studies = inst, series, []string{canon}
	case "patient":
		inst, err := s.ids(`SELECT i.id FROM instances i JOIN series se ON se.id = i.series_id JOIN studies st ON st.id = se.study_id WHERE st.patient_id = ?`, canon)
		if err != nil {
			return removed{}, err
		}
		series, err := s.ids(`SELECT se.id FROM series se JOIN studies st ON st.id = se.study_id WHERE st.patient_id = ?`, canon)
		if err != nil {
			return removed{}, err
		}
		studies, err := s.ids(`SELECT id FROM studies WHERE patient_id = ?`, canon)
		if err != nil {
			return removed{}, err
		}
		out.instances, out.series, out.studies, out.patients = inst, series, studies, []string{canon}
	default:
		return removed{}, ErrNotFound
	}
	return out, nil
}

// Changes returns events with Seq greater than since, oldest first.
// last is the sequence a client should pass as the next since. done is true
// when nothing remains after last.
func (s *Store) Changes(since int64, limit int) ([]Change, int64, bool, error) {
	if limit <= 0 || limit > 10000 {
		limit = 100
	}
	if since < 0 {
		since = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT seq, change_type, resource_type, resource_id, created_at FROM changes WHERE seq > ? ORDER BY seq LIMIT ?`, since, limit)
	if err != nil {
		return nil, 0, false, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.Seq, &c.Type, &c.ResourceType, &c.ID, &c.Date); err != nil {
			return nil, 0, false, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, err
	}
	if out == nil {
		out = []Change{}
	}
	last := since
	if len(out) > 0 {
		last = out[len(out)-1].Seq
	}
	var more int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM changes WHERE seq > ?`, last).Scan(&more); err != nil {
		return nil, 0, false, err
	}
	return out, last, more == 0, nil
}

// ClearChanges drops the changes log. Later events keep increasing Seq.
func (s *Store) ClearChanges() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM changes`)
	return err
}

// GlobalStatistics counts every stored resource and sums file sizes.
func (s *Store) GlobalStatistics() (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st Stats
	st.Global = true
	st.ShowPatients, st.ShowStudies, st.ShowSeries, st.ShowInstances = true, true, true, true
	err := s.db.QueryRow(`
SELECT
  (SELECT COUNT(*) FROM patients),
  (SELECT COUNT(*) FROM studies),
  (SELECT COUNT(*) FROM series),
  (SELECT COUNT(*) FROM instances),
  COALESCE((SELECT SUM(file_size) FROM instances), 0)`).Scan(&st.Patients, &st.Studies, &st.Series, &st.Instances, &st.Disk)
	return st, err
}

// ResourceStatistics counts resources under id. Sizes are the stored Part 10 bytes.
func (s *Store) ResourceStatistics(kind, resourceID string) (Stats, error) {
	canon, ok := id.Canonical(resourceID)
	if !ok {
		return Stats{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.exists(kind, canon); err != nil {
		return Stats{}, err
	}
	var st Stats
	var err error
	switch kind {
	case "patient":
		st.ShowStudies, st.ShowSeries, st.ShowInstances = true, true, true
		err = s.db.QueryRow(`
SELECT
  (SELECT COUNT(*) FROM studies WHERE patient_id = ?),
  (SELECT COUNT(*) FROM series se JOIN studies st ON st.id = se.study_id WHERE st.patient_id = ?),
  (SELECT COUNT(*) FROM instances i JOIN series se ON se.id = i.series_id JOIN studies st ON st.id = se.study_id WHERE st.patient_id = ?),
  COALESCE((SELECT SUM(i.file_size) FROM instances i JOIN series se ON se.id = i.series_id JOIN studies st ON st.id = se.study_id WHERE st.patient_id = ?), 0)`,
			canon, canon, canon, canon).Scan(&st.Studies, &st.Series, &st.Instances, &st.Disk)
	case "study":
		st.ShowSeries, st.ShowInstances = true, true
		err = s.db.QueryRow(`
SELECT
  (SELECT COUNT(*) FROM series WHERE study_id = ?),
  (SELECT COUNT(*) FROM instances i JOIN series se ON se.id = i.series_id WHERE se.study_id = ?),
  COALESCE((SELECT SUM(i.file_size) FROM instances i JOIN series se ON se.id = i.series_id WHERE se.study_id = ?), 0)`,
			canon, canon, canon).Scan(&st.Series, &st.Instances, &st.Disk)
	case "series":
		st.ShowInstances = true
		err = s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(file_size), 0) FROM instances WHERE series_id = ?`, canon).Scan(&st.Instances, &st.Disk)
	case "instance":
		err = s.db.QueryRow(`SELECT file_size FROM instances WHERE id = ?`, canon).Scan(&st.Disk)
	default:
		return Stats{}, ErrNotFound
	}
	return st, err
}

// ArchiveFiles lists the DICOM files under a resource, in study/series/instance order.
func (s *Store) ArchiveFiles(kind, resourceID string) ([]ArchiveFile, error) {
	canon, ok := id.Canonical(resourceID)
	if !ok {
		return nil, ErrNotFound
	}
	where, err := archiveWhere(kind)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.exists(kind, canon); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
SELECT p.dicom_id, st.study_uid, se.series_uid, i.sop_uid, i.file_uuid
FROM instances i
JOIN series se ON se.id = i.series_id
JOIN studies st ON st.id = se.study_id
JOIN patients p ON p.id = st.patient_id
WHERE `+where+`
ORDER BY st.study_uid, se.series_uid, i.index_in_series, i.sop_uid`, canon)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ArchiveFile
	for rows.Next() {
		var patient, study, series, sop, uuid string
		if err := rows.Scan(&patient, &study, &series, &sop, &uuid); err != nil {
			return nil, err
		}
		out = append(out, ArchiveFile{
			Name: zipComponent(patient) + "/" + zipComponent(study) + "/" + zipComponent(series) + "/" + zipComponent(sop) + ".dcm",
			Path: s.filePath(uuid),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []ArchiveFile{}
	}
	return out, nil
}

func archiveWhere(kind string) (string, error) {
	switch kind {
	case "patient":
		return `p.id = ?`, nil
	case "study":
		return `st.id = ?`, nil
	case "series":
		return `se.id = ?`, nil
	case "instance":
		return `i.id = ?`, nil
	default:
		return "", ErrNotFound
	}
}

func zipComponent(s string) string {
	s = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		default:
			return r
		}
	}, strings.TrimSpace(s))
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}
