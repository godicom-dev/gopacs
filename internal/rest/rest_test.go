package rest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gopacs/internal/archive"
)

func TestUploadAndReadBack(t *testing.T) {
	st, err := archive.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := Handler{Store: st, System: System{Name: "gopacs", Version: "0.1.0", DicomAet: "GOPACS", DicomPort: 4242, HttpPort: 8042}}.Routes()
	srv := httptest.NewServer(h)
	defer srv.Close()

	raw := part10(t)
	res, err := http.Post(srv.URL+"/instances", "application/dicom", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body := readJSON(t, res)
	if body["Status"] != "Success" || body["ParentPatient"] != "07a6ec1c-1be5920b-18ef5358-d24441f3-10e926ea" {
		t.Fatalf("%v", body)
	}
	instance := body["ID"].(string)

	res, err = http.Get(srv.URL + "/patients")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	decode(t, res, &ids)
	if len(ids) != 1 || ids[0] != body["ParentPatient"] {
		t.Fatalf("patients %v", ids)
	}

	res, err = http.Get(srv.URL + "/instances/" + instance + "/simplified-tags")
	if err != nil {
		t.Fatal(err)
	}
	var tags map[string]any
	decode(t, res, &tags)
	if tags["PatientID"] != "000000185" || tags["SOPInstanceUID"] != "1.2.3.4.5.6" {
		t.Fatalf("tags %v", tags)
	}

	res, err = http.Post(srv.URL+"/tools/lookup", "text/plain", bytes.NewBufferString("1.2.3.4.5.6"))
	if err != nil {
		t.Fatal(err)
	}
	var hits []map[string]string
	decode(t, res, &hits)
	if len(hits) != 1 || hits[0]["Type"] != "Instance" || hits[0]["Path"] != "/instances/"+instance {
		t.Fatalf("lookup %v", hits)
	}

	res, err = http.Post(srv.URL+"/tools/find", "application/json", bytes.NewBufferString(`{"Level":"Study","Query":{"PatientID":"000000185"},"Expand":true}`))
	if err != nil {
		t.Fatal(err)
	}
	var found []map[string]any
	decode(t, res, &found)
	if len(found) != 1 || found[0]["Type"] != "Study" {
		t.Fatalf("find %v", found)
	}

	res, err = http.Get(srv.URL + "/instances/" + instance + "/file")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || len(got) != len(raw) {
		t.Fatalf("file status %d len %d", res.StatusCode, len(got))
	}

	res, err = http.Post(srv.URL+"/instances", "application/dicom", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	again := readJSON(t, res)
	if again["Status"] != "AlreadyStored" {
		t.Fatalf("second upload %v", again)
	}
}

func part10(t *testing.T) []byte {
	t.Helper()
	ds := godicom.NewDataset()
	for _, pair := range []struct {
		tg tag.Tag
		v  string
	}{
		{tag.PatientID, "000000185"},
		{tag.PatientName, "Anonymous^Unknown"},
		{tag.StudyInstanceUID, "1.2.840.113704.1.111.7016.1342451220.40"},
		{tag.SeriesInstanceUID, "1.2.3.4.5"},
		{tag.SOPInstanceUID, "1.2.3.4.5.6"},
		{tag.SOPClassUID, string(uid.CTImageStorage)},
		{tag.Modality, "CT"},
	} {
		if err := ds.SetString(pair.tg, pair.v); err != nil {
			t.Fatal(err)
		}
	}
	fd := &godicom.FileDataset{Dataset: ds, FileMeta: godicom.NewFileMetaDataset()}
	raw, err := godicom.EncodeFile(fd, &godicom.WriteOptions{EnforceFileFormat: true})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func readJSON(t *testing.T, res *http.Response) map[string]any {
	t.Helper()
	defer res.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		t.Fatalf("status %d body %v", res.StatusCode, out)
	}
	return out
}

func decode(t *testing.T, res *http.Response, dest any) {
	t.Helper()
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("status %d %s", res.StatusCode, b)
	}
	if err := json.NewDecoder(res.Body).Decode(dest); err != nil {
		t.Fatal(err)
	}
}
