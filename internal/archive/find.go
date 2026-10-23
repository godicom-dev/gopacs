package archive

import (
	"fmt"
	"regexp"
	"strings"
)

// Find returns resources at level whose tags match query. An empty query value
// matches anything. "*" and "?" are wildcards. A DICOM date range (YYYYMMDD-YYYYMMDD)
// is honored for tags whose name ends in Date.
func (s *Store) Find(level string, query map[string]string) ([]Hit, error) {
	level = normalizeLevel(level)
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.scanLevel(level)
	if err != nil {
		return nil, err
	}
	var out []Hit
	for _, row := range rows {
		if matchQuery(query, row.Tags) {
			out = append(out, row)
		}
	}
	if out == nil {
		out = []Hit{}
	}
	return out, nil
}

func (s *Store) scanLevel(level string) ([]Hit, error) {
	var q string
	switch level {
	case LevelPatient:
		q = `SELECT id, main_tags FROM patients ORDER BY id`
	case LevelStudy:
		q = `SELECT st.id, st.main_tags, p.main_tags FROM studies st JOIN patients p ON p.id = st.patient_id ORDER BY st.id`
	case LevelSeries:
		q = `SELECT se.id, se.main_tags, st.main_tags, p.main_tags
FROM series se JOIN studies st ON st.id = se.study_id JOIN patients p ON p.id = st.patient_id ORDER BY se.id`
	case LevelImage:
		q = `SELECT i.id, i.main_tags, se.main_tags, st.main_tags, p.main_tags
FROM instances i JOIN series se ON se.id = i.series_id JOIN studies st ON st.id = se.study_id JOIN patients p ON p.id = st.patient_id
ORDER BY i.id`
	default:
		return nil, fmt.Errorf("archive: unknown level %q", level)
	}
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		switch level {
		case LevelPatient:
			var id, tags string
			if err := rows.Scan(&id, &tags); err != nil {
				return nil, err
			}
			out = append(out, Hit{Kind: "patient", ID: id, Tags: decodeTags(tags)})
		case LevelStudy:
			var id, own, parent string
			if err := rows.Scan(&id, &own, &parent); err != nil {
				return nil, err
			}
			out = append(out, Hit{Kind: "study", ID: id, Tags: mergeTags(decodeTags(parent), decodeTags(own))})
		case LevelSeries:
			var id, own, study, patient string
			if err := rows.Scan(&id, &own, &study, &patient); err != nil {
				return nil, err
			}
			out = append(out, Hit{Kind: "series", ID: id, Tags: mergeTags(decodeTags(patient), decodeTags(study), decodeTags(own))})
		default:
			var id, own, series, study, patient string
			if err := rows.Scan(&id, &own, &series, &study, &patient); err != nil {
				return nil, err
			}
			out = append(out, Hit{Kind: "instance", ID: id, Tags: mergeTags(decodeTags(patient), decodeTags(study), decodeTags(series), decodeTags(own))})
		}
	}
	return out, rows.Err()
}

func normalizeLevel(level string) string {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "PATIENT":
		return LevelPatient
	case "STUDY":
		return LevelStudy
	case "SERIES":
		return LevelSeries
	case "IMAGE", "INSTANCE":
		return LevelImage
	default:
		return strings.ToUpper(strings.TrimSpace(level))
	}
}

func matchQuery(query, tags map[string]string) bool {
	for key, pattern := range query {
		if !dicomMatch(pattern, tags[key]) {
			return false
		}
	}
	return true
}

func dicomMatch(pattern, value string) bool {
	pattern = strings.TrimRight(pattern, " \x00")
	if pattern == "" || pattern == "*" {
		return true
	}
	if strings.Contains(value, `\`) {
		for _, part := range strings.Split(value, `\`) {
			if dicomMatch(pattern, part) {
				return true
			}
		}
		return false
	}
	if lo, hi, ok := dateRange(pattern); ok && len(value) >= 8 {
		day := value[:8]
		return day >= lo && day <= hi
	}
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteByte('.')
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteByte('$')
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(value)
}

func dateRange(pattern string) (string, string, bool) {
	dash := strings.IndexByte(pattern, '-')
	if dash < 0 {
		return "", "", false
	}
	lo, hi := pattern[:dash], pattern[dash+1:]
	if lo == "" {
		lo = "00000000"
	}
	if hi == "" {
		hi = "99999999"
	}
	if len(lo) != 8 || len(hi) != 8 {
		return "", "", false
	}
	for _, c := range lo + hi {
		if c < '0' || c > '9' {
			return "", "", false
		}
	}
	return lo, hi, true
}
