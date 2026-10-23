package id

import "testing"

func TestDeriveMatchesOrthancBook(t *testing.T) {
	// Orthanc Book, "Accessing a patient" / "Browsing from the patient down
	// to the instance": PatientID 000000185 and that study UID.
	got := Derive("000000185", "1.2.840.113704.1.111.7016.1342451220.40", "", "")
	if got.Patient != "07a6ec1c-1be5920b-18ef5358-d24441f3-10e926ea" {
		t.Fatalf("patient id %s", got.Patient)
	}
	if got.Study != "9ad2b0da-a406c43c-6e0df76d-1204b86f-78d12c15" {
		t.Fatalf("study id %s", got.Study)
	}
}

func TestCanonicalAcceptsDashes(t *testing.T) {
	in := "07a6ec1c1be5920b18ef5358d24441f310e926ea"
	got, ok := Canonical(in)
	if !ok || got != "07a6ec1c-1be5920b-18ef5358-d24441f3-10e926ea" {
		t.Fatalf("got %q ok %v", got, ok)
	}
	if _, ok := Canonical("not-an-id"); ok {
		t.Fatal("accepted a short id")
	}
}
