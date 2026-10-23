package archive

import (
	"path/filepath"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/godicom/uid"
)

func TestPutListLookupFindDelete(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	raw := samplePart10(t, "000000185", "1.2.840.113704.1.111.7016.1342451220.40", "1.2.3.4.5", "1.2.3.4.5.6")
	got, err := st.Put(raw, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.PatientID != "07a6ec1c-1be5920b-18ef5358-d24441f3-10e926ea" {
		t.Fatalf("patient %s", got.PatientID)
	}
	if got.StudyID != "9ad2b0da-a406c43c-6e0df76d-1204b86f-78d12c15" {
		t.Fatalf("study %s", got.StudyID)
	}
	again, err := st.Put(raw, false)
	if err != nil || !again.AlreadyStored || again.InstanceID != got.InstanceID {
		t.Fatalf("second put %+v %v", again, err)
	}

	patients, err := st.List("patient")
	if err != nil || len(patients) != 1 || patients[0] != got.PatientID {
		t.Fatalf("patients %v %v", patients, err)
	}
	studies, err := st.Children("patient", got.PatientID, "study")
	if err != nil || len(studies) != 1 || studies[0] != got.StudyID {
		t.Fatalf("studies %v %v", studies, err)
	}

	hits, err := st.Lookup("1.2.3.4.5.6")
	if err != nil || len(hits) != 1 || hits[0].ID != got.InstanceID || hits[0].Kind != "instance" {
		t.Fatalf("lookup %+v %v", hits, err)
	}
	found, err := st.Find(LevelStudy, map[string]string{"PatientName": "Anonymous*"})
	if err != nil || len(found) != 1 || found[0].ID != got.StudyID {
		t.Fatalf("find %+v %v", found, err)
	}
	none, err := st.Find(LevelStudy, map[string]string{"StudyDate": "19990101-19991231"})
	if err != nil || len(none) != 0 {
		t.Fatalf("date range %+v %v", none, err)
	}

	file, err := st.InstanceFile(got.InstanceID)
	if err != nil || len(file) != len(raw) {
		t.Fatalf("file %d %v", len(file), err)
	}
	if err := st.Delete("patient", got.PatientID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get("instance", got.InstanceID); err != ErrNotFound {
		t.Fatalf("after delete: %v", err)
	}
}

func TestPutRejectsMissingIdentity(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ds := godicom.NewDataset()
	if err := ds.SetString(tag.PatientName, "DOE"); err != nil {
		t.Fatal(err)
	}
	fd := &godicom.FileDataset{Dataset: ds, FileMeta: godicom.NewFileMetaDataset()}
	if err := ds.SetString(tag.SOPClassUID, string(uid.CTImageStorage)); err != nil {
		t.Fatal(err)
	}
	if err := ds.SetString(tag.SOPInstanceUID, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	raw, err := godicom.EncodeFile(fd, &godicom.WriteOptions{EnforceFileFormat: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(raw, false); err != ErrMissingIdentity {
		t.Fatalf("got %v", err)
	}
}

func samplePart10(t *testing.T, patientID, studyUID, seriesUID, sopUID string) []byte {
	t.Helper()
	ds := godicom.NewDataset()
	set := func(tg tag.Tag, v string) {
		t.Helper()
		if err := ds.SetString(tg, v); err != nil {
			t.Fatal(err)
		}
	}
	set(tag.PatientID, patientID)
	set(tag.PatientName, "Anonymous^Unknown")
	set(tag.StudyInstanceUID, studyUID)
	set(tag.StudyDate, "20120716")
	set(tag.SeriesInstanceUID, seriesUID)
	set(tag.Modality, "CT")
	set(tag.SOPInstanceUID, sopUID)
	set(tag.SOPClassUID, string(uid.CTImageStorage))
	set(tag.InstanceNumber, "6")
	fd := &godicom.FileDataset{Dataset: ds, FileMeta: godicom.NewFileMetaDataset()}
	raw, err := godicom.EncodeFile(fd, &godicom.WriteOptions{EnforceFileFormat: true})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
