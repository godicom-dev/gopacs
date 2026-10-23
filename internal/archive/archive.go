// Package archive stores DICOM instances as Part 10 files plus a SQLite index
// keyed by Orthanc identifiers.
package archive

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gopacs/internal/id"
	_ "modernc.org/sqlite"
)

// Levels used by C-FIND (IMAGE) and by Orthanc's /tools/find (Instance).
const (
	LevelPatient  = "PATIENT"
	LevelStudy    = "STUDY"
	LevelSeries   = "SERIES"
	LevelImage    = "IMAGE"
	LevelInstance = "INSTANCE"
)

var (
	ErrNotFound        = errors.New("archive: resource not found")
	ErrMissingIdentity = errors.New("archive: PatientID, StudyInstanceUID, SeriesInstanceUID and SOPInstanceUID are required")
	ErrNotDICOM        = errors.New("archive: not a DICOM Part 10 file")
	ErrInvalidModality = errors.New("archive: invalid modality")
	ErrDuplicateAET    = errors.New("archive: AE title already registered")
)

// PutResult is what a successful store produced.
type PutResult struct {
	PatientID     string
	StudyID       string
	SeriesID      string
	InstanceID    string
	AlreadyStored bool
}

// Resource is one indexed patient, study, series or instance.
type Resource struct {
	Kind                      string
	ID                        string
	LastUpdate                time.Time
	MainDicomTags             map[string]string
	PatientMainDicomTags      map[string]string
	Children                  []string
	ParentPatient             string
	ParentStudy               string
	ParentSeries              string
	FileSize                  int64
	FileUUID                  string
	IndexInSeries             int
	ExpectedNumberOfInstances *int
	InstanceCount             int
}

// Hit is one C-FIND or /tools/find match.
type Hit struct {
	Kind string
	ID   string
	Tags map[string]string
}

// Store is the on-disk archive. Open it once and share it.
type Store struct {
	mu   sync.Mutex
	db   *sql.DB
	root string
}

// Open creates dir if needed and opens (or creates) the index inside it.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(filepath.Join(dir, "index.db")) + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, root: dir}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS patients (
  id TEXT PRIMARY KEY,
  dicom_id TEXT NOT NULL,
  main_tags TEXT NOT NULL,
  last_update TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS studies (
  id TEXT PRIMARY KEY,
  patient_id TEXT NOT NULL REFERENCES patients(id) ON DELETE CASCADE,
  study_uid TEXT NOT NULL,
  main_tags TEXT NOT NULL,
  last_update TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS series (
  id TEXT PRIMARY KEY,
  study_id TEXT NOT NULL REFERENCES studies(id) ON DELETE CASCADE,
  series_uid TEXT NOT NULL,
  main_tags TEXT NOT NULL,
  expected_instances INTEGER,
  last_update TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS instances (
  id TEXT PRIMARY KEY,
  series_id TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE,
  sop_uid TEXT NOT NULL,
  file_uuid TEXT NOT NULL,
  file_size INTEGER NOT NULL,
  index_in_series INTEGER NOT NULL,
  main_tags TEXT NOT NULL,
  last_update TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS patients_dicom ON patients(dicom_id);
CREATE INDEX IF NOT EXISTS studies_uid ON studies(study_uid);
CREATE INDEX IF NOT EXISTS studies_patient ON studies(patient_id);
CREATE INDEX IF NOT EXISTS series_uid ON series(series_uid);
CREATE INDEX IF NOT EXISTS series_study ON series(study_id);
CREATE INDEX IF NOT EXISTS instances_uid ON instances(sop_uid);
CREATE INDEX IF NOT EXISTS instances_series ON instances(series_id);
CREATE TABLE IF NOT EXISTS changes (
  seq INTEGER PRIMARY KEY AUTOINCREMENT,
  change_type TEXT NOT NULL,
  resource_type TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS modalities (
  name TEXT PRIMARY KEY,
  aet TEXT NOT NULL,
  host TEXT NOT NULL,
  port INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS modalities_aet ON modalities(aet);
`)
	return err
}

// Put indexes a Part 10 file. overwrite replaces an instance that already has
// the same Orthanc identifier; otherwise the first copy is kept.
func (s *Store) Put(part10 []byte, overwrite bool) (PutResult, error) {
	fd, err := godicom.ReadBytes(part10, nil)
	if err != nil {
		return PutResult{}, fmt.Errorf("%w: %v", ErrNotDICOM, err)
	}
	ident, err := readIdentity(fd.Dataset)
	if err != nil {
		return PutResult{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)

	s.mu.Lock()
	defer s.mu.Unlock()

	var existingUUID string
	err = s.db.QueryRow(`SELECT file_uuid FROM instances WHERE id = ?`, ident.instance).Scan(&existingUUID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PutResult{}, err
	}
	if err == nil && !overwrite {
		return PutResult{
			PatientID: ident.patient, StudyID: ident.study, SeriesID: ident.series, InstanceID: ident.instance,
			AlreadyStored: true,
		}, nil
	}

	fileUUID := existingUUID
	if fileUUID == "" {
		fileUUID = newUUID()
	}
	path := s.filePath(fileUUID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, part10, 0o644); err != nil {
		return PutResult{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		_ = os.Remove(tmp)
		return PutResult{}, err
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
			_ = os.Remove(tmp)
		}
	}()

	newPatient, err := notExists(tx, "patients", ident.patient)
	if err != nil {
		return PutResult{}, err
	}
	newStudy, err := notExists(tx, "studies", ident.study)
	if err != nil {
		return PutResult{}, err
	}
	newSeries, err := notExists(tx, "series", ident.series)
	if err != nil {
		return PutResult{}, err
	}

	if err := upsertParent(tx, `
INSERT INTO patients(id, dicom_id, main_tags, last_update) VALUES(?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET last_update = excluded.last_update`,
		ident.patient, ident.dicomPatient, ident.patientTags, now); err != nil {
		return PutResult{}, err
	}
	if err := upsertParent(tx, `
INSERT INTO studies(id, patient_id, study_uid, main_tags, last_update) VALUES(?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET last_update = excluded.last_update`,
		ident.study, ident.patient, ident.studyUID, ident.studyTags, now); err != nil {
		return PutResult{}, err
	}
	if err := upsertSeries(tx, ident, now); err != nil {
		return PutResult{}, err
	}
	tags, err := mustJSON(ident.instanceTags)
	if err != nil {
		return PutResult{}, err
	}
	if existingUUID == "" {
		_, err = tx.Exec(`INSERT INTO instances(id, series_id, sop_uid, file_uuid, file_size, index_in_series, main_tags, last_update)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
			ident.instance, ident.series, ident.sopUID, fileUUID, len(part10), ident.indexInSeries, tags, now)
	} else {
		_, err = tx.Exec(`UPDATE instances SET file_size = ?, index_in_series = ?, main_tags = ?, last_update = ? WHERE id = ?`,
			len(part10), ident.indexInSeries, tags, now, ident.instance)
	}
	if err != nil {
		return PutResult{}, err
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	if existingUUID == "" {
		if err := recordChange(tx, "NewInstance", "Instance", ident.instance, stamp); err != nil {
			return PutResult{}, err
		}
		if newSeries {
			if err := recordChange(tx, "NewSeries", "Series", ident.series, stamp); err != nil {
				return PutResult{}, err
			}
		}
		if newStudy {
			if err := recordChange(tx, "NewStudy", "Study", ident.study, stamp); err != nil {
				return PutResult{}, err
			}
		}
		if newPatient {
			if err := recordChange(tx, "NewPatient", "Patient", ident.patient, stamp); err != nil {
				return PutResult{}, err
			}
		}
	} else if err := recordChange(tx, "UpdatedAttachment", "Instance", ident.instance, stamp); err != nil {
		return PutResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return PutResult{}, err
	}
	rollback = false
	if err := os.Rename(tmp, path); err != nil {
		return PutResult{}, err
	}
	return PutResult{
		PatientID: ident.patient, StudyID: ident.study, SeriesID: ident.series, InstanceID: ident.instance,
	}, nil
}

func upsertParent(tx *sql.Tx, query string, args ...any) error {
	tags, err := mustJSON(args[len(args)-2].(map[string]string))
	if err != nil {
		return err
	}
	args[len(args)-2] = tags
	_, err = tx.Exec(query, args...)
	return err
}

func upsertSeries(tx *sql.Tx, ident identity, now string) error {
	tags, err := mustJSON(ident.seriesTags)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
INSERT INTO series(id, study_id, series_uid, main_tags, expected_instances, last_update) VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET last_update = excluded.last_update,
  expected_instances = COALESCE(excluded.expected_instances, series.expected_instances)`,
		ident.series, ident.study, ident.seriesUID, tags, ident.expected, now)
	return err
}

// List returns Orthanc identifiers at kind (patient, study, series, instance).
func (s *Store) List(kind string) ([]string, error) {
	table, err := tableFor(kind)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id FROM ` + table + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if out == nil {
		out = []string{}
	}
	return out, rows.Err()
}

// Get loads one resource. kind is patient, study, series or instance.
func (s *Store) Get(kind, resourceID string) (Resource, error) {
	canon, ok := id.Canonical(resourceID)
	if !ok {
		return Resource{}, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch kind {
	case "patient":
		return s.patient(canon)
	case "study":
		return s.study(canon)
	case "series":
		return s.series(canon)
	case "instance":
		return s.instance(canon)
	default:
		return Resource{}, fmt.Errorf("archive: unknown kind %q", kind)
	}
}

func (s *Store) patient(canon string) (Resource, error) {
	var dicomID, tags, updated string
	err := s.db.QueryRow(`SELECT dicom_id, main_tags, last_update FROM patients WHERE id = ?`, canon).Scan(&dicomID, &tags, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Resource{}, ErrNotFound
	}
	if err != nil {
		return Resource{}, err
	}
	kids, err := s.ids(`SELECT id FROM studies WHERE patient_id = ? ORDER BY id`, canon)
	if err != nil {
		return Resource{}, err
	}
	return Resource{Kind: "patient", ID: canon, LastUpdate: parseTime(updated), MainDicomTags: decodeTags(tags), Children: kids}, nil
}

func (s *Store) study(canon string) (Resource, error) {
	var patientID, tags, updated string
	err := s.db.QueryRow(`SELECT patient_id, main_tags, last_update FROM studies WHERE id = ?`, canon).Scan(&patientID, &tags, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Resource{}, ErrNotFound
	}
	if err != nil {
		return Resource{}, err
	}
	parent, err := s.patient(patientID)
	if err != nil {
		return Resource{}, err
	}
	kids, err := s.ids(`SELECT id FROM series WHERE study_id = ? ORDER BY id`, canon)
	if err != nil {
		return Resource{}, err
	}
	return Resource{
		Kind: "study", ID: canon, LastUpdate: parseTime(updated), MainDicomTags: decodeTags(tags),
		PatientMainDicomTags: parent.MainDicomTags, Children: kids, ParentPatient: patientID,
	}, nil
}

func (s *Store) series(canon string) (Resource, error) {
	var studyID, tags, updated string
	var expected sql.NullInt64
	err := s.db.QueryRow(`SELECT study_id, main_tags, expected_instances, last_update FROM series WHERE id = ?`, canon).
		Scan(&studyID, &tags, &expected, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Resource{}, ErrNotFound
	}
	if err != nil {
		return Resource{}, err
	}
	kids, err := s.ids(`SELECT id FROM instances WHERE series_id = ? ORDER BY index_in_series, id`, canon)
	if err != nil {
		return Resource{}, err
	}
	res := Resource{
		Kind: "series", ID: canon, LastUpdate: parseTime(updated), MainDicomTags: decodeTags(tags),
		Children: kids, ParentStudy: studyID, InstanceCount: len(kids),
	}
	if expected.Valid {
		n := int(expected.Int64)
		res.ExpectedNumberOfInstances = &n
	}
	return res, nil
}

func (s *Store) instance(canon string) (Resource, error) {
	var seriesID, uuid, tags, updated string
	var size int64
	var index int
	err := s.db.QueryRow(`SELECT series_id, file_uuid, file_size, index_in_series, main_tags, last_update FROM instances WHERE id = ?`, canon).
		Scan(&seriesID, &uuid, &size, &index, &tags, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Resource{}, ErrNotFound
	}
	if err != nil {
		return Resource{}, err
	}
	return Resource{
		Kind: "instance", ID: canon, LastUpdate: parseTime(updated), MainDicomTags: decodeTags(tags),
		ParentSeries: seriesID, FileSize: size, FileUUID: uuid, IndexInSeries: index,
	}, nil
}

// Children lists the Orthanc identifiers directly under resourceID.
func (s *Store) Children(kind, resourceID, child string) ([]string, error) {
	canon, ok := id.Canonical(resourceID)
	if !ok {
		return nil, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.exists(kind, canon); err != nil {
		return nil, err
	}
	switch kind + "/" + child {
	case "patient/study":
		return s.ids(`SELECT id FROM studies WHERE patient_id = ? ORDER BY id`, canon)
	case "study/series":
		return s.ids(`SELECT id FROM series WHERE study_id = ? ORDER BY id`, canon)
	case "study/instance":
		return s.ids(`SELECT i.id FROM instances i JOIN series se ON se.id = i.series_id WHERE se.study_id = ? ORDER BY i.index_in_series, i.id`, canon)
	case "series/instance":
		return s.ids(`SELECT id FROM instances WHERE series_id = ? ORDER BY index_in_series, id`, canon)
	default:
		return nil, ErrNotFound
	}
}

// Parent returns the parent resource id of kind parent ("patient", "study", "series").
func (s *Store) Parent(kind, resourceID, parent string) (string, error) {
	canon, ok := id.Canonical(resourceID)
	if !ok {
		return "", ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var q string
	switch kind + "/" + parent {
	case "study/patient":
		q = `SELECT patient_id FROM studies WHERE id = ?`
	case "series/study":
		q = `SELECT study_id FROM series WHERE id = ?`
	case "series/patient":
		q = `SELECT st.patient_id FROM series se JOIN studies st ON st.id = se.study_id WHERE se.id = ?`
	case "instance/series":
		q = `SELECT series_id FROM instances WHERE id = ?`
	case "instance/study":
		q = `SELECT se.study_id FROM instances i JOIN series se ON se.id = i.series_id WHERE i.id = ?`
	case "instance/patient":
		q = `SELECT st.patient_id FROM instances i JOIN series se ON se.id = i.series_id JOIN studies st ON st.id = se.study_id WHERE i.id = ?`
	default:
		return "", ErrNotFound
	}
	var id string
	err := s.db.QueryRow(q, canon).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// Part10ByUID reads one instance addressed by its DICOM UIDs.
func (s *Store) Part10ByUID(studyUID, seriesUID, sopUID string) ([]byte, error) {
	parts, err := s.readPart10s(`
SELECT i.file_uuid
FROM instances i
JOIN series se ON se.id = i.series_id
JOIN studies st ON st.id = se.study_id
WHERE st.study_uid = ? AND se.series_uid = ? AND i.sop_uid = ?`, studyUID, seriesUID, sopUID)
	if err != nil {
		return nil, err
	}
	return parts[0], nil
}

// Part10sByUID reads every instance in a study. An empty seriesUID selects the whole study.
func (s *Store) Part10sByUID(studyUID, seriesUID string) ([][]byte, error) {
	return s.readPart10s(`
SELECT i.file_uuid
FROM instances i
JOIN series se ON se.id = i.series_id
JOIN studies st ON st.id = se.study_id
WHERE st.study_uid = ? AND (? = '' OR se.series_uid = ?)
ORDER BY se.series_uid, i.index_in_series, i.sop_uid`, studyUID, seriesUID, seriesUID)
}

func (s *Store) readPart10s(query string, args ...any) ([][]byte, error) {
	s.mu.Lock()
	rows, err := s.db.Query(query, args...)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	var uuids []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			rows.Close()
			s.mu.Unlock()
			return nil, err
		}
		uuids = append(uuids, uuid)
	}
	err = rows.Err()
	rows.Close()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if len(uuids) == 0 {
		return nil, ErrNotFound
	}
	out := make([][]byte, 0, len(uuids))
	for _, uuid := range uuids {
		raw, err := os.ReadFile(s.filePath(uuid))
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

// InstanceFile reads the stored Part 10 bytes.
func (s *Store) InstanceFile(resourceID string) ([]byte, error) {
	res, err := s.Get("instance", resourceID)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(s.filePath(res.FileUUID))
}

// InstanceDataset reads and parses the stored Part 10 file.
func (s *Store) InstanceDataset(resourceID string) (*godicom.FileDataset, error) {
	raw, err := s.InstanceFile(resourceID)
	if err != nil {
		return nil, err
	}
	return godicom.ReadBytes(raw, nil)
}

// Lookup finds resources whose Orthanc identifier or DICOM UID/Patient ID equals key.
func (s *Store) Lookup(key string) ([]Hit, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return []Hit{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Hit
	if canon, ok := id.Canonical(key); ok {
		for _, kind := range []string{"patient", "study", "series", "instance"} {
			if _, err := s.exists(kind, canon); err == nil {
				out = append(out, Hit{Kind: kind, ID: canon})
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	queries := []struct {
		kind string
		q    string
	}{
		{"patient", `SELECT id FROM patients WHERE dicom_id = ?`},
		{"study", `SELECT id FROM studies WHERE study_uid = ?`},
		{"series", `SELECT id FROM series WHERE series_uid = ?`},
		{"instance", `SELECT id FROM instances WHERE sop_uid = ?`},
	}
	for _, item := range queries {
		ids, err := s.ids(item.q, key)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			out = append(out, Hit{Kind: item.kind, ID: id})
		}
	}
	if out == nil {
		out = []Hit{}
	}
	return out, nil
}


// Delete removes a resource and everything under it, including files.
func (s *Store) Delete(kind, resourceID string) error {
	canon, ok := id.Canonical(resourceID)
	if !ok {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.exists(kind, canon); err != nil {
		return err
	}
	files, err := s.filesUnder(kind, canon)
	if err != nil {
		return err
	}
	doomed, err := s.removedIDs(kind, canon)
	if err != nil {
		return err
	}
	table, err := tableFor(kind)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM `+table+` WHERE id = ?`, canon); err != nil {
		_ = tx.Rollback()
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	for _, id := range doomed.instances {
		if err := recordChange(tx, "DeletedInstance", "Instance", id, stamp); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, id := range doomed.series {
		if err := recordChange(tx, "DeletedSeries", "Series", id, stamp); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, id := range doomed.studies {
		if err := recordChange(tx, "DeletedStudy", "Study", id, stamp); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	for _, id := range doomed.patients {
		if err := recordChange(tx, "DeletedPatient", "Patient", id, stamp); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, uuid := range files {
		_ = os.Remove(s.filePath(uuid))
	}
	return nil
}

func (s *Store) filesUnder(kind, canon string) ([]string, error) {
	var q string
	switch kind {
	case "instance":
		q = `SELECT file_uuid FROM instances WHERE id = ?`
	case "series":
		q = `SELECT file_uuid FROM instances WHERE series_id = ?`
	case "study":
		q = `SELECT i.file_uuid FROM instances i JOIN series se ON se.id = i.series_id WHERE se.study_id = ?`
	case "patient":
		q = `SELECT i.file_uuid FROM instances i JOIN series se ON se.id = i.series_id JOIN studies st ON st.id = se.study_id WHERE st.patient_id = ?`
	default:
		return nil, ErrNotFound
	}
	return s.ids(q, canon)
}

func (s *Store) exists(kind, canon string) (string, error) {
	table, err := tableFor(kind)
	if err != nil {
		return "", err
	}
	var id string
	err = s.db.QueryRow(`SELECT id FROM `+table+` WHERE id = ?`, canon).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (s *Store) ids(query string, arg string) ([]string, error) {
	rows, err := s.db.Query(query, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}


func (s *Store) filePath(uuid string) string {
	return filepath.Join(s.root, "files", uuid+".dcm")
}


func tableFor(kind string) (string, error) {
	switch kind {
	case "patient":
		return "patients", nil
	case "study":
		return "studies", nil
	case "series":
		return "series", nil
	case "instance":
		return "instances", nil
	default:
		return "", fmt.Errorf("archive: unknown kind %q", kind)
	}
}


func mustJSON(tags map[string]string) (string, error) {
	if tags == nil {
		tags = map[string]string{}
	}
	raw, err := json.Marshal(tags)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func decodeTags(raw string) map[string]string {
	out := map[string]string{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	if out == nil {
		out = map[string]string{}
	}
	return out
}

func mergeTags(parts ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, part := range parts {
		for k, v := range part {
			out[k] = v
		}
	}
	return out
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}


func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

