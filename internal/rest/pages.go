package rest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gopacs/internal/archive"
)

type uiPage struct {
	SystemName   string
	Nav          string
	StatsLine    string
	Title        string
	Message      string
	Filter       string
	Searched     bool
	Kind         string
	Crumbs       []uiCrumb
	Tags         []uiTag
	Rows         []uiRow
	Empty        string
	ShowChildren bool
	OHIF         string
	Download     string
	Zip          string
	Delete       string
	Next         string
}

type uiCrumb struct {
	Href  string
	Label string
}

type uiTag struct {
	Name  string
	Value string
}

type uiRow struct {
	Href  string
	Title string
	Sub   string
	Side  string
}

func (h Handler) render(w http.ResponseWriter, code int, name string, p uiPage) {
	if p.SystemName == "" {
		p.SystemName = h.System.Name
		if p.SystemName == "" {
			p.SystemName = "gopacs"
		}
	}
	if st, err := h.Store.GlobalStatistics(); err == nil {
		p.StatsLine = fmt.Sprintf("%d patients · %d studies · %d instances · %d MB",
			st.Patients, st.Studies, st.Instances, st.Disk/(1024*1024))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = pageTmpl.ExecuteTemplate(w, name, p)
}

func (h Handler) uiFail(w http.ResponseWriter, err error) {
	if errors.Is(err, archive.ErrNotFound) {
		h.render(w, http.StatusNotFound, "message", uiPage{Title: "Not found", Message: "resource not found"})
		return
	}
	h.render(w, http.StatusInternalServerError, "message", uiPage{Title: "Error", Message: "internal error"})
}

func (h Handler) uiPatients(w http.ResponseWriter, r *http.Request) {
	h.uiList(w, r, "patient", "patients", "All patients", "Nothing stored")
}

func (h Handler) uiStudies(w http.ResponseWriter, r *http.Request) {
	h.uiList(w, r, "study", "studies", "All studies", "Nothing stored")
}

func (h Handler) uiList(w http.ResponseWriter, r *http.Request, kind, nav, title, empty string) {
	ids, err := h.Store.List(kind)
	if err != nil {
		h.uiFail(w, err)
		return
	}
	filter := strings.TrimSpace(r.URL.Query().Get("q"))
	rows := make([]uiRow, 0, len(ids))
	for _, id := range ids {
		res, err := h.Store.Get(kind, id)
		if err != nil {
			h.uiFail(w, err)
			return
		}
		row := resourceRow(res)
		if filter != "" && !strings.Contains(strings.ToLower(row.Title+" "+row.Sub+" "+row.Side), strings.ToLower(filter)) {
			continue
		}
		rows = append(rows, row)
	}
	h.render(w, http.StatusOK, "list", uiPage{
		Nav: nav, Title: title, Filter: filter, Rows: rows, Empty: empty,
	})
}

func (h Handler) uiPatient(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	patient, err := h.Store.Get("patient", id)
	if err != nil {
		h.uiFail(w, err)
		return
	}
	studies, err := h.childRows(id, "patient", "study")
	if err != nil {
		h.uiFail(w, err)
		return
	}
	h.render(w, http.StatusOK, "detail", uiPage{
		Nav: "patients", Title: orDefault(mainTag(patient, "PatientName"), "Patient"),
		Crumbs: []uiCrumb{{Href: "/app/patients", Label: "All patients"}},
		Tags:   tagRows(patient.MainDicomTags),
		Rows:   studies, Empty: "No studies", ShowChildren: true,
		Kind: "patient", Zip: "/patients/" + patient.ID + "/archive",
		Delete: "/patients/" + patient.ID, Next: "/app/patients",
	})
}

func (h Handler) uiStudy(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	study, err := h.Store.Get("study", id)
	if err != nil {
		h.uiFail(w, err)
		return
	}
	series, err := h.childRows(id, "study", "series")
	if err != nil {
		h.uiFail(w, err)
		return
	}
	title := mainTag(study, "StudyDescription")
	if title == "" {
		title = dicomDay(mainTag(study, "StudyDate"))
	}
	h.render(w, http.StatusOK, "detail", uiPage{
		Title: orDefault(title, "Study"),
		Crumbs: []uiCrumb{
			{Href: "/app/patients/" + study.ParentPatient, Label: orDefault(patientTag(study, "PatientName"), "Patient")},
			{Label: " · Study"},
		},
		Tags: tagRows(study.MainDicomTags), Rows: series, Empty: "No series", ShowChildren: true,
		Kind: "study", Zip: "/studies/" + study.ID + "/archive",
		Delete: "/studies/" + study.ID, Next: "/app/patients/" + study.ParentPatient,
		OHIF: "/ohif/viewer?StudyInstanceUIDs=" + url.QueryEscape(mainTag(study, "StudyInstanceUID")),
	})
}

func (h Handler) uiSeries(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	series, err := h.Store.Get("series", id)
	if err != nil {
		h.uiFail(w, err)
		return
	}
	studyID, err := h.Store.Parent("series", id, "study")
	if err != nil {
		h.uiFail(w, err)
		return
	}
	study, err := h.Store.Get("study", studyID)
	if err != nil {
		h.uiFail(w, err)
		return
	}
	instances, err := h.childRows(id, "series", "instance")
	if err != nil {
		h.uiFail(w, err)
		return
	}
	h.render(w, http.StatusOK, "detail", uiPage{
		Title: orDefault(orDefault(mainTag(series, "SeriesDescription"), mainTag(series, "Modality")), "Series"),
		Crumbs: []uiCrumb{
			{Href: "/app/patients/" + study.ParentPatient, Label: orDefault(patientTag(study, "PatientName"), "Patient")},
			{Label: " · "},
			{Href: "/app/studies/" + study.ID, Label: orDefault(mainTag(study, "StudyDescription"), "Study")},
			{Label: " · Series"},
		},
		Tags: tagRows(series.MainDicomTags), Rows: instances, Empty: "No instances", ShowChildren: true,
		Kind: "series", Zip: "/series/" + series.ID + "/archive",
		Delete: "/series/" + series.ID, Next: "/app/studies/" + study.ID,
	})
}

func (h Handler) uiInstance(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	instance, err := h.Store.Get("instance", id)
	if err != nil {
		h.uiFail(w, err)
		return
	}
	seriesID, err := h.Store.Parent("instance", id, "series")
	if err != nil {
		h.uiFail(w, err)
		return
	}
	series, err := h.Store.Get("series", seriesID)
	if err != nil {
		h.uiFail(w, err)
		return
	}
	ds, err := h.Store.InstanceDataset(id)
	if err != nil {
		h.uiFail(w, err)
		return
	}
	h.render(w, http.StatusOK, "detail", uiPage{
		Title: orDefault(mainTag(instance, "SOPInstanceUID"), "Instance"),
		Crumbs: []uiCrumb{
			{Href: "/app/series/" + series.ID, Label: orDefault(mainTag(series, "SeriesDescription"), "Series")},
			{Label: " · Instance"},
		},
		Tags:     datasetTags(ds),
		Kind:     "instance",
		Download: "/instances/" + instance.ID + "/file",
		Zip:      "/instances/" + instance.ID + "/archive",
		Delete:   "/instances/" + instance.ID,
		Next:     "/app/series/" + series.ID,
	})
}

func (h Handler) uiUpload(w http.ResponseWriter, r *http.Request) {
	h.render(w, http.StatusOK, "upload", uiPage{Nav: "upload"})
}

func (h Handler) uiLookup(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	p := uiPage{Nav: "lookup", Filter: q, Empty: "No match"}
	if q != "" {
		p.Searched = true
		hits, err := h.Store.Lookup(q)
		if err != nil {
			h.uiFail(w, err)
			return
		}
		p.Rows = make([]uiRow, 0, len(hits))
		for _, hit := range hits {
			p.Rows = append(p.Rows, uiRow{
				Href: "/app/" + plural(hit.Kind) + "/" + hit.ID, Title: orthancType(hit.Kind), Side: hit.ID,
			})
		}
	}
	h.render(w, http.StatusOK, "lookup", p)
}

func (h Handler) childRows(id, kind, child string) ([]uiRow, error) {
	ids, err := h.Store.Children(kind, id, child)
	if err != nil {
		return nil, err
	}
	rows := make([]uiRow, 0, len(ids))
	for _, childID := range ids {
		res, err := h.Store.Get(child, childID)
		if err != nil {
			return nil, err
		}
		rows = append(rows, resourceRow(res))
	}
	return rows, nil
}

func resourceRow(res archive.Resource) uiRow {
	switch res.Kind {
	case "patient":
		n := len(res.Children)
		side := fmt.Sprintf("%d studies", n)
		if n == 1 {
			side = "1 study"
		}
		return uiRow{
			Href: "/app/patients/" + res.ID, Title: orDefault(mainTag(res, "PatientName"), "(no name)"),
			Sub: mainTag(res, "PatientID"), Side: side,
		}
	case "study":
		when := dicomDay(mainTag(res, "StudyDate"))
		side := fmt.Sprintf("%d series", len(res.Children))
		if when != "" {
			side = when + " · " + side
		}
		return uiRow{
			Href: "/app/studies/" + res.ID, Title: orDefault(mainTag(res, "StudyDescription"), mainTag(res, "StudyInstanceUID")),
			Sub: orDefault(patientTag(res, "PatientName"), "(no name)") + " · " + patientTag(res, "PatientID"), Side: side,
		}
	case "series":
		n := len(res.Children)
		side := fmt.Sprintf("%d instances", n)
		if n == 1 {
			side = "1 instance"
		}
		label := strings.TrimSpace(mainTag(res, "Modality") + " " + orDefault(mainTag(res, "SeriesDescription"), mainTag(res, "SeriesInstanceUID")))
		return uiRow{Href: "/app/series/" + res.ID, Title: label, Side: side}
	default:
		title := mainTag(res, "SOPInstanceUID")
		if n := mainTag(res, "InstanceNumber"); n != "" {
			title = "Instance " + n
		}
		return uiRow{Href: "/app/instances/" + res.ID, Title: title, Side: fmt.Sprintf("%d bytes", res.FileSize)}
	}
}

func mainTag(res archive.Resource, key string) string {
	if res.MainDicomTags == nil {
		return ""
	}
	return res.MainDicomTags[key]
}

func patientTag(res archive.Resource, key string) string {
	if v := res.PatientMainDicomTags[key]; v != "" {
		return v
	}
	return mainTag(res, key)
}

func tagRows(tags map[string]string) []uiTag {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]uiTag, 0, len(keys))
	for _, key := range keys {
		out = append(out, uiTag{Name: key, Value: tags[key]})
	}
	return out
}

func datasetTags(ds *godicom.FileDataset) []uiTag {
	if ds == nil {
		return nil
	}
	raw := simplifiedTags(ds.Dataset)
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]uiTag, 0, len(keys))
	for _, key := range keys {
		out = append(out, uiTag{Name: key, Value: tagValue(raw[key])})
	}
	return out
}

func tagValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		raw, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

func dicomDay(v string) string {
	if len(v) < 8 {
		return v
	}
	return v[:4] + "-" + v[4:6] + "-" + v[6:8]
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
