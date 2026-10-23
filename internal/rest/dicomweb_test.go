package rest

import (
	"bytes"
	"context"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/gonetdicom/dicomweb"
	"github.com/godicom-dev/gopacs/internal/archive"
)

func TestDICOMwebRoundTrip(t *testing.T) {
	st, err := archive.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	srv := httptest.NewServer(Handler{Store: st, MaxUpload: 8 << 20}.Routes())
	defer srv.Close()
	client := &dicomweb.Client{BaseURL: srv.URL + "/dicom-web", HTTPClient: srv.Client()}
	ctx := context.Background()

	raw := part10(t)
	stored, err := client.StoreInstances(ctx, "", [][]byte{raw})
	if err != nil || stored.StatusCode != 200 {
		t.Fatalf("stow %+v %v", stored, err)
	}

	studies, err := client.SearchStudies(ctx, url.Values{"PatientID": {"000000185"}})
	if err != nil || len(studies) != 1 {
		t.Fatalf("studies %d %v", len(studies), err)
	}
	studyUID, _ := studies[0].GetString(tag.StudyInstanceUID)
	if studyUID != "1.2.840.113704.1.111.7016.1342451220.40" {
		t.Fatalf("study %q", studyUID)
	}

	series, err := client.SearchSeries(ctx, studyUID, url.Values{"Modality": {"CT"}})
	if err != nil || len(series) != 1 {
		t.Fatalf("series %d %v", len(series), err)
	}
	seriesUID, _ := series[0].GetString(tag.SeriesInstanceUID)

	got, err := client.RetrieveInstance(ctx, studyUID, seriesUID, "1.2.3.4.5.6")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("retrieved %d bytes, stored %d", len(got), len(raw))
	}

	studyParts, err := client.RetrieveStudy(ctx, studyUID)
	if err != nil || len(studyParts) != 1 || !bytes.Equal(studyParts[0], raw) {
		t.Fatalf("study retrieve %d %v", len(studyParts), err)
	}
	meta, err := client.RetrieveInstanceMetadata(ctx, studyUID, seriesUID, "1.2.3.4.5.6")
	if err != nil {
		t.Fatal(err)
	}
	if sop, _ := meta.GetString(tag.SOPInstanceUID); sop != "1.2.3.4.5.6" {
		t.Fatalf("metadata sop %q", sop)
	}

	miss, err := client.SearchStudies(ctx, url.Values{"PatientID": {"nobody"}})
	if err != nil || len(miss) != 0 {
		t.Fatalf("miss %v %v", miss, err)
	}
	if _, err := client.StoreInstances(ctx, "1.2.9.9", [][]byte{raw}); err == nil {
		t.Fatal("stow accepted a study uid that is not in the file")
	}

	res, err := srv.Client().Get(srv.URL + "/instances")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	decode(t, res, &ids)
	if len(ids) != 1 {
		t.Fatalf("orthanc instances %v", ids)
	}
}
