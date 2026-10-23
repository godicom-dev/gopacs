package rest

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/godicom-dev/gopacs/internal/archive"
)

func TestStatisticsChangesAndArchive(t *testing.T) {
	st, err := archive.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := httptest.NewServer(Handler{Store: st}.Routes())
	defer srv.Close()

	raw := part10(t)
	res, err := http.Post(srv.URL+"/instances", "application/dicom", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	stored := readJSON(t, res)
	study := stored["ParentStudy"].(string)

	res, err = http.Get(srv.URL + "/statistics")
	if err != nil {
		t.Fatal(err)
	}
	stats := readJSON(t, res)
	if stats["CountPatients"] != float64(1) || stats["CountInstances"] != float64(1) {
		t.Fatalf("statistics %v", stats)
	}
	if stats["TotalDiskSize"] != strconv.Itoa(len(raw)) || stats["TotalUncompressedSize"] != stats["TotalDiskSize"] {
		t.Fatalf("disk %v", stats)
	}

	res, err = http.Get(srv.URL + "/studies/" + study + "/statistics")
	if err != nil {
		t.Fatal(err)
	}
	one := readJSON(t, res)
	if one["CountSeries"] != float64(1) || one["CountInstances"] != float64(1) {
		t.Fatalf("study statistics %v", one)
	}
	if _, ok := one["CountStudies"]; ok {
		t.Fatalf("study statistics include CountStudies: %v", one)
	}

	res, err = http.Get(srv.URL + "/changes?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	log := readJSON(t, res)
	changes := log["Changes"].([]any)
	if len(changes) != 4 || log["Done"] != true {
		t.Fatalf("changes %v", log)
	}
	want := []string{"NewInstance", "NewSeries", "NewStudy", "NewPatient"}
	for i, kind := range want {
		if changes[i].(map[string]any)["ChangeType"] != kind {
			t.Fatalf("change %d %v", i, changes[i])
		}
	}

	res, err = http.Get(srv.URL + "/studies/" + study + "/archive")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("archive %d %v", res.StatusCode, err)
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "000000185/1.2.840.113704.1.111.7016.1342451220.40/1.2.3.4.5/1.2.3.4.5.6.dcm" {
		t.Fatalf("zip names %v", names(zr))
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("zip bytes %d %v", len(got), err)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/changes", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	readJSON(t, res)
	res, err = http.Get(srv.URL + "/changes")
	if err != nil {
		t.Fatal(err)
	}
	empty := readJSON(t, res)
	if n := len(empty["Changes"].([]any)); n != 0 || empty["Done"] != true {
		t.Fatalf("cleared %v", empty)
	}
}

func names(zr *zip.Reader) []string {
	out := make([]string, len(zr.File))
	for i, f := range zr.File {
		out[i] = f.Name
	}
	return out
}
