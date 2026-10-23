package rest

import (
	"errors"
	"fmt"
	"net/url"
	"sort"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/gonetdicom/dicomweb"
	"github.com/godicom-dev/gopacs/internal/archive"
)

// webStore adapts the archive to the DICOMweb Studies Service.
type webStore struct {
	store     *archive.Store
	overwrite bool
}

func (a webStore) GetInstance(studyUID, seriesUID, instanceUID string) ([]byte, error) {
	raw, err := a.store.Part10ByUID(studyUID, seriesUID, instanceUID)
	if errors.Is(err, archive.ErrNotFound) {
		return nil, dicomweb.ErrNotFound
	}
	return raw, err
}

func (a webStore) ListInstances(studyUID, seriesUID string) ([][]byte, error) {
	parts, err := a.store.Part10sByUID(studyUID, seriesUID)
	if errors.Is(err, archive.ErrNotFound) {
		return nil, dicomweb.ErrNotFound
	}
	return parts, err
}

func (a webStore) PutInstance(studyUID string, part10 []byte) error {
	if studyUID != "" {
		fd, err := godicom.ReadBytes(part10, nil)
		if err != nil {
			return err
		}
		got, _ := fd.GetString(tag.StudyInstanceUID)
		if got != studyUID {
			return fmt.Errorf("dicomweb: StudyInstanceUID mismatch")
		}
	}
	_, err := a.store.Put(part10, a.overwrite)
	return err
}

func (a webStore) SearchStudies(query url.Values) ([]*godicom.Dataset, error) {
	hits, err := a.store.Find(archive.LevelStudy, nil)
	if err != nil {
		return nil, err
	}
	keys := []string{"StudyInstanceUID", "PatientID", "PatientName", "StudyDate"}
	seen := map[string]*godicom.Dataset{}
	for _, hit := range hits {
		if !exact(hit.Tags, query, keys...) {
			continue
		}
		uid := hit.Tags["StudyInstanceUID"]
		if uid == "" || seen[uid] != nil {
			continue
		}
		seen[uid] = project(hit.Tags, keys...)
	}
	return sorted(seen, "StudyInstanceUID"), nil
}

func (a webStore) SearchSeries(studyUID string, query url.Values) ([]*godicom.Dataset, error) {
	hits, err := a.store.Find(archive.LevelSeries, nil)
	if err != nil {
		return nil, err
	}
	keys := []string{"StudyInstanceUID", "SeriesInstanceUID", "Modality", "SeriesNumber"}
	seen := map[string]*godicom.Dataset{}
	for _, hit := range hits {
		if studyUID != "" && hit.Tags["StudyInstanceUID"] != studyUID {
			continue
		}
		if !exact(hit.Tags, query, "SeriesInstanceUID", "Modality", "SeriesNumber") {
			continue
		}
		uid := hit.Tags["SeriesInstanceUID"]
		if uid == "" || seen[uid] != nil {
			continue
		}
		seen[uid] = project(hit.Tags, keys...)
	}
	return sorted(seen, "SeriesInstanceUID"), nil
}

func (a webStore) SearchInstances(studyUID, seriesUID string, query url.Values) ([]*godicom.Dataset, error) {
	hits, err := a.store.Find(archive.LevelImage, nil)
	if err != nil {
		return nil, err
	}
	keys := []string{"StudyInstanceUID", "SeriesInstanceUID", "SOPInstanceUID", "SOPClassUID", "InstanceNumber", "Modality"}
	var out []*godicom.Dataset
	for _, hit := range hits {
		if studyUID != "" && hit.Tags["StudyInstanceUID"] != studyUID {
			continue
		}
		if seriesUID != "" && hit.Tags["SeriesInstanceUID"] != seriesUID {
			continue
		}
		if !exact(hit.Tags, query, "SOPInstanceUID", "SOPClassUID", "InstanceNumber", "Modality") {
			continue
		}
		out = append(out, project(hit.Tags, keys...))
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i].GetString(tag.SOPInstanceUID)
		b, _ := out[j].GetString(tag.SOPInstanceUID)
		return a < b
	})
	return out, nil
}

func exact(tags map[string]string, query url.Values, keys ...string) bool {
	for _, key := range keys {
		want := query.Get(key)
		if want == "" {
			continue
		}
		if tags[key] != want {
			return false
		}
	}
	return true
}

func project(tags map[string]string, keys ...string) *godicom.Dataset {
	ds := godicom.NewDataset()
	for _, key := range keys {
		value := tags[key]
		if value == "" {
			continue
		}
		tg, ok := tag.ByKeyword(key)
		if !ok {
			continue
		}
		_ = ds.SetString(tg, value)
	}
	return ds
}

func sorted(seen map[string]*godicom.Dataset, keyword string) []*godicom.Dataset {
	tg, _ := tag.ByKeyword(keyword)
	out := make([]*godicom.Dataset, 0, len(seen))
	for _, ds := range seen {
		out = append(out, ds)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i].GetString(tg)
		b, _ := out[j].GetString(tg)
		return a < b
	})
	return out
}
