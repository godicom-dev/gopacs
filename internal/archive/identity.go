package archive

import (
	"strconv"
	"strings"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/gopacs/internal/id"
)

type identity struct {
	patient, study, series, instance          string
	dicomPatient, studyUID, seriesUID, sopUID string
	patientTags, studyTags, seriesTags        map[string]string
	instanceTags                              map[string]string
	expected                                  *int
	indexInSeries                             int
}

func readIdentity(ds *godicom.Dataset) (identity, error) {
	patientID, ok1 := ds.GetString(tag.PatientID)
	studyUID, ok2 := ds.GetString(tag.StudyInstanceUID)
	seriesUID, ok3 := ds.GetString(tag.SeriesInstanceUID)
	sopUID, ok4 := ds.GetString(tag.SOPInstanceUID)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return identity{}, ErrMissingIdentity
	}
	derived := id.Derive(patientID, studyUID, seriesUID, sopUID)
	var expected *int
	if raw, ok := ds.GetString(tag.NumberOfSeriesRelatedInstances); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			expected = &n
		}
	}
	index := 0
	if raw, ok := ds.GetString(tag.InstanceNumber); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			index = n
		}
	}
	return identity{
		patient: derived.Patient, study: derived.Study, series: derived.Series, instance: derived.Instance,
		dicomPatient: strings.TrimRight(patientID, " \x00"),
		studyUID:     strings.TrimRight(studyUID, " \x00"),
		seriesUID:    strings.TrimRight(seriesUID, " \x00"),
		sopUID:       strings.TrimRight(sopUID, " \x00"),
		patientTags:  pick(ds, tag.PatientID, tag.PatientName, tag.PatientBirthDate, tag.PatientSex, tag.OtherPatientIDs),
		studyTags:    pick(ds, tag.StudyInstanceUID, tag.StudyDate, tag.StudyTime, tag.StudyID, tag.StudyDescription, tag.AccessionNumber),
		seriesTags: pick(ds, tag.SeriesInstanceUID, tag.SeriesNumber, tag.SeriesDescription, tag.Modality, tag.SeriesDate, tag.SeriesTime,
			tag.Manufacturer, tag.StationName, tag.ProtocolName),
		instanceTags:  pick(ds, tag.SOPClassUID, tag.SOPInstanceUID, tag.InstanceNumber, tag.ImageIndex, tag.InstanceCreationDate, tag.InstanceCreationTime),
		expected:      expected,
		indexInSeries: index,
	}, nil
}

func pick(ds *godicom.Dataset, tags ...tag.Tag) map[string]string {
	out := map[string]string{}
	for _, tg := range tags {
		v, ok := ds.GetString(tg)
		v = strings.TrimRight(v, " \x00")
		if !ok || v == "" {
			continue
		}
		name, ok := tag.Keyword(tg)
		if !ok {
			continue
		}
		out[name] = v
	}
	return out
}
