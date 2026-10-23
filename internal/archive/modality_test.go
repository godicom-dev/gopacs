package archive

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestModalities(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	m := Modality{Name: "pacs", AET: "PACS", Host: "127.0.0.1", Port: 4242}
	if err := st.PutModality(m); err != nil {
		t.Fatal(err)
	}
	got, err := st.Modality("pacs")
	if err != nil || got != m {
		t.Fatalf("get %+v %v", got, err)
	}
	byAET, err := st.ModalityByAET("PACS ")
	if err != nil || byAET.Name != "pacs" {
		t.Fatalf("aet %+v %v", byAET, err)
	}
	if err := st.PutModality(Modality{Name: "other", AET: "PACS", Host: "127.0.0.1", Port: 11112}); !errors.Is(err, ErrDuplicateAET) {
		t.Fatalf("duplicate %v", err)
	}
	if err := st.PutModality(Modality{Name: "pacs", AET: "PACS2", Host: "localhost", Port: 104}); err != nil {
		t.Fatal(err)
	}
	list, err := st.Modalities()
	if err != nil || len(list) != 1 || list[0].AET != "PACS2" || list[0].Port != 104 {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := st.PutModality(Modality{Name: "bad name", AET: "X", Host: "127.0.0.1", Port: 1}); !errors.Is(err, ErrInvalidModality) {
		t.Fatalf("name %v", err)
	}
	if err := st.PutModality(Modality{Name: "x", AET: stringsTooLong(), Host: "127.0.0.1", Port: 1}); !errors.Is(err, ErrInvalidModality) {
		t.Fatalf("aet %v", err)
	}
	if err := st.DeleteModality("pacs"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Modality("pacs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted %v", err)
	}
}

func stringsTooLong() string {
	return "ABCDEFGHIJKLMNOPQ"
}
