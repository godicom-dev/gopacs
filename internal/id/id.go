// Package id derives Orthanc resource identifiers.
//
// Patients, studies, series and instances are named by a SHA-1 of their DICOM
// identity tags, with "|" between tags, then shown as 40 lowercase hex
// characters grouped 8-8-8-8-8. That is the scheme documented in the Orthanc
// Book (Orthanc identifiers).
package id

import (
	"crypto/sha1"
	"strings"
)

// Resource is one level of the Patient/Study/Series/Instance tree.
type Resource struct {
	Patient  string
	Study    string
	Series   string
	Instance string
}

// Derive hashes the four DICOM identity values. Empty strings are hashed as
// empty; the caller decides whether a missing tag is an error.
func Derive(patientID, studyUID, seriesUID, sopUID string) Resource {
	patientID = trim(patientID)
	studyUID = trim(studyUID)
	seriesUID = trim(seriesUID)
	sopUID = trim(sopUID)
	return Resource{
		Patient:  format(patientID),
		Study:    format(patientID + "|" + studyUID),
		Series:   format(patientID + "|" + studyUID + "|" + seriesUID),
		Instance: format(patientID + "|" + studyUID + "|" + seriesUID + "|" + sopUID),
	}
}

// Canonical accepts an Orthanc identifier with or without dashes.
func Canonical(s string) (string, bool) {
	raw := strings.ToLower(strings.ReplaceAll(s, "-", ""))
	if len(raw) != 40 {
		return "", false
	}
	for _, c := range raw {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return group(raw), true
}

func format(s string) string {
	sum := sha1.Sum([]byte(s))
	const hexdigits = "0123456789abcdef"
	var raw [40]byte
	for i, b := range sum {
		raw[i*2] = hexdigits[b>>4]
		raw[i*2+1] = hexdigits[b&0x0f]
	}
	return group(string(raw[:]))
}

func group(raw string) string {
	return raw[0:8] + "-" + raw[8:16] + "-" + raw[16:24] + "-" + raw[24:32] + "-" + raw[32:40]
}

func trim(s string) string {
	return strings.TrimRight(s, " \x00")
}
