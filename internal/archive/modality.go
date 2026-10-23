package archive

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Modality is a remote Application Entity. C-MOVE looks the destination up by
// AE Title. The REST name is the Orthanc modality name.
type Modality struct {
	Name string `json:"-"`
	AET  string `json:"AET"`
	Host string `json:"Host"`
	Port int    `json:"Port"`
}

// PutModality creates or replaces the modality named m.Name.
func (s *Store) PutModality(m Modality) error {
	m, err := validModality(m)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var owner string
	err = s.db.QueryRow(`SELECT name FROM modalities WHERE aet = ? AND name != ?`, m.AET, m.Name).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		return ErrDuplicateAET
	}
	_, err = s.db.Exec(`
INSERT INTO modalities (name, aet, host, port) VALUES (?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET aet = excluded.aet, host = excluded.host, port = excluded.port`,
		m.Name, m.AET, m.Host, m.Port)
	return err
}

// DeleteModality removes a modality by its REST name.
func (s *Store) DeleteModality(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM modalities WHERE name = ?`, name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Modality returns one modality by its REST name.
func (s *Store) Modality(name string) (Modality, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modality(`SELECT name, aet, host, port FROM modalities WHERE name = ?`, name)
}

// ModalityByAET returns the modality registered under an AE Title.
// Comparison ignores trailing spaces, as a C-MOVE Move Destination does.
func (s *Store) ModalityByAET(aet string) (Modality, error) {
	aet = strings.TrimRight(aet, " ")
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modality(`SELECT name, aet, host, port FROM modalities WHERE aet = ?`, aet)
}

// Modalities lists every modality, ordered by name.
func (s *Store) Modalities() ([]Modality, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT name, aet, host, port FROM modalities ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Modality
	for rows.Next() {
		var m Modality
		if err := rows.Scan(&m.Name, &m.AET, &m.Host, &m.Port); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Modality{}
	}
	return out, nil
}

func (s *Store) modality(query string, arg string) (Modality, error) {
	var m Modality
	err := s.db.QueryRow(query, arg).Scan(&m.Name, &m.AET, &m.Host, &m.Port)
	if errors.Is(err, sql.ErrNoRows) {
		return Modality{}, ErrNotFound
	}
	return m, err
}

func validModality(m Modality) (Modality, error) {
	m.Name = strings.TrimSpace(m.Name)
	m.AET = strings.TrimRight(strings.TrimLeft(m.AET, " "), " ")
	m.Host = strings.TrimSpace(m.Host)
	if !validModalityName(m.Name) {
		return Modality{}, fmt.Errorf("%w: name", ErrInvalidModality)
	}
	if !validAETitle(m.AET) {
		return Modality{}, fmt.Errorf("%w: AE title", ErrInvalidModality)
	}
	if m.Host == "" || strings.ContainsAny(m.Host, " /\\") {
		return Modality{}, fmt.Errorf("%w: host", ErrInvalidModality)
	}
	if m.Port < 1 || m.Port > 65535 {
		return Modality{}, fmt.Errorf("%w: port", ErrInvalidModality)
	}
	return m, nil
}

func validModalityName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if r > unicode.MaxASCII || (!unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '_' && r != '-') {
			return false
		}
	}
	return true
}

func validAETitle(aet string) bool {
	if aet == "" || len(aet) > 16 {
		return false
	}
	for _, r := range aet {
		if r < 0x20 || r > 0x7e || r == '\\' {
			return false
		}
	}
	return true
}
