// Package rest serves the Orthanc-compatible routes gopacs implements.
package rest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/tag"
	"github.com/godicom-dev/gopacs/internal/archive"
)

// System is the GET /system document.
type System struct {
	Name                  string `json:"Name"`
	Version               string `json:"Version"`
	ApiVersion            int    `json:"ApiVersion"`
	DicomAet              string `json:"DicomAet"`
	DicomPort             int    `json:"DicomPort"`
	HttpPort              int    `json:"HttpPort"`
	OverwriteInstances    bool   `json:"OverwriteInstances"`
	StorageAreaPlugin     any    `json:"StorageAreaPlugin"`
	DatabaseBackendPlugin any    `json:"DatabaseBackendPlugin"`
}

// Handler is the HTTP API. Mount it at the root of the server.
type Handler struct {
	Store     *archive.Store
	System    System
	Overwrite bool
	MaxUpload int64
}

// Routes returns the Orthanc-shaped routes this server implements.
func (h Handler) Routes() http.Handler {
	max := h.MaxUpload
	if max <= 0 {
		max = 256 << 20
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /system", h.system)
	mux.HandleFunc("GET /patients", h.list("patient"))
	mux.HandleFunc("GET /studies", h.list("study"))
	mux.HandleFunc("GET /series", h.list("series"))
	mux.HandleFunc("GET /instances", h.list("instance"))

	mux.HandleFunc("GET /patients/{id}", h.one("patient"))
	mux.HandleFunc("GET /studies/{id}", h.one("study"))
	mux.HandleFunc("GET /series/{id}", h.one("series"))
	mux.HandleFunc("GET /instances/{id}", h.one("instance"))

	mux.HandleFunc("DELETE /patients/{id}", h.remove("patient"))
	mux.HandleFunc("DELETE /studies/{id}", h.remove("study"))
	mux.HandleFunc("DELETE /series/{id}", h.remove("series"))
	mux.HandleFunc("DELETE /instances/{id}", h.remove("instance"))

	mux.HandleFunc("GET /patients/{id}/studies", h.children("patient", "study"))
	mux.HandleFunc("GET /studies/{id}/series", h.children("study", "series"))
	mux.HandleFunc("GET /studies/{id}/instances", h.children("study", "instance"))
	mux.HandleFunc("GET /series/{id}/instances", h.children("series", "instance"))

	mux.HandleFunc("GET /studies/{id}/patient", h.parent("study", "patient"))
	mux.HandleFunc("GET /series/{id}/study", h.parent("series", "study"))
	mux.HandleFunc("GET /series/{id}/patient", h.parent("series", "patient"))
	mux.HandleFunc("GET /instances/{id}/series", h.parent("instance", "series"))
	mux.HandleFunc("GET /instances/{id}/study", h.parent("instance", "study"))
	mux.HandleFunc("GET /instances/{id}/patient", h.parent("instance", "patient"))

	mux.HandleFunc("GET /instances/{id}/file", h.file)
	mux.HandleFunc("GET /instances/{id}/simplified-tags", h.simplified)
	mux.HandleFunc("GET /instances/{id}/tags", h.tags)
	mux.HandleFunc("GET /instances/{id}/content/{tag}", h.content)

	mux.HandleFunc("POST /instances", h.upload(max))
	mux.HandleFunc("POST /tools/lookup", h.lookup(max))
	mux.HandleFunc("POST /tools/find", h.find(max))
	return mux
}

func (h Handler) system(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.System)
}

func (h Handler) list(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids, err := h.Store.List(kind)
		if err != nil {
			writeArchiveError(w, r, err)
			return
		}
		if !expand(r) {
			writeJSON(w, http.StatusOK, ids)
			return
		}
		out := make([]any, 0, len(ids))
		for _, id := range ids {
			res, err := h.Store.Get(kind, id)
			if err != nil {
				writeArchiveError(w, r, err)
				return
			}
			out = append(out, h.view(res))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (h Handler) one(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := h.Store.Get(kind, r.PathValue("id"))
		if err != nil {
			writeArchiveError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, h.view(res))
	}
}

func (h Handler) remove(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h.Store.Delete(kind, r.PathValue("id")); err != nil {
			writeArchiveError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{})
	}
}

func (h Handler) children(kind, child string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids, err := h.Store.Children(kind, r.PathValue("id"), child)
		if err != nil {
			writeArchiveError(w, r, err)
			return
		}
		if !expand(r) {
			writeJSON(w, http.StatusOK, ids)
			return
		}
		out := make([]any, 0, len(ids))
		for _, id := range ids {
			res, err := h.Store.Get(child, id)
			if err != nil {
				writeArchiveError(w, r, err)
				return
			}
			out = append(out, h.view(res))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (h Handler) parent(kind, parent string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := h.Store.Parent(kind, r.PathValue("id"), parent)
		if err != nil {
			writeArchiveError(w, r, err)
			return
		}
		res, err := h.Store.Get(parent, id)
		if err != nil {
			writeArchiveError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, h.view(res))
	}
}

func (h Handler) upload(max int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
		if err != nil {
			writeError(w, r, http.StatusRequestEntityTooLarge, "upload is larger than the configured limit")
			return
		}
		stored, err := h.Store.Put(body, h.Overwrite)
		if err != nil {
			writeArchiveError(w, r, err)
			return
		}
		status := "Success"
		if stored.AlreadyStored {
			status = "AlreadyStored"
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"ID":            stored.InstanceID,
			"ParentPatient": stored.PatientID,
			"ParentStudy":   stored.StudyID,
			"ParentSeries":  stored.SeriesID,
			"Path":          "/instances/" + stored.InstanceID,
			"Status":        status,
		})
	}
}

func (h Handler) file(w http.ResponseWriter, r *http.Request) {
	raw, err := h.Store.InstanceFile(r.PathValue("id"))
	if err != nil {
		writeArchiveError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/dicom")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (h Handler) simplified(w http.ResponseWriter, r *http.Request) {
	ds, err := h.Store.InstanceDataset(r.PathValue("id"))
	if err != nil {
		writeArchiveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, simplifiedTags(ds.Dataset))
}

func (h Handler) tags(w http.ResponseWriter, r *http.Request) {
	ds, err := h.Store.InstanceDataset(r.PathValue("id"))
	if err != nil {
		writeArchiveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, detailedTags(ds.Dataset))
}

func (h Handler) content(w http.ResponseWriter, r *http.Request) {
	ds, err := h.Store.InstanceDataset(r.PathValue("id"))
	if err != nil {
		writeArchiveError(w, r, err)
		return
	}
	tg, err := parseTag(r.PathValue("tag"))
	if err != nil {
		writeError(w, r, http.StatusNotFound, "unknown tag")
		return
	}
	value, ok := ds.GetString(tg)
	if !ok {
		writeError(w, r, http.StatusNotFound, "tag is not present")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, value)
}

func (h Handler) lookup(max int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
		if err != nil {
			writeError(w, r, http.StatusRequestEntityTooLarge, "lookup body is too large")
			return
		}
		hits, err := h.Store.Lookup(string(body))
		if err != nil {
			writeArchiveError(w, r, err)
			return
		}
		out := make([]map[string]string, 0, len(hits))
		for _, hit := range hits {
			kind := orthancType(hit.Kind)
			out = append(out, map[string]string{
				"ID":   hit.ID,
				"Path": "/" + plural(hit.Kind) + "/" + hit.ID,
				"Type": kind,
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (h Handler) find(max int64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
		if err != nil {
			writeError(w, r, http.StatusRequestEntityTooLarge, "find body is too large")
			return
		}
		var req struct {
			Level  string         `json:"Level"`
			Query  map[string]any `json:"Query"`
			Expand bool           `json:"Expand"`
			Limit  int            `json:"Limit"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeError(w, r, http.StatusBadRequest, "find body must be a JSON object")
			return
		}
		if req.Level == "" {
			writeError(w, r, http.StatusBadRequest, "Level is required")
			return
		}
		query := map[string]string{}
		for k, v := range req.Query {
			query[k] = stringify(v)
		}
		hits, err := h.Store.Find(req.Level, query)
		if err != nil {
			writeArchiveError(w, r, err)
			return
		}
		if req.Limit > 0 && len(hits) > req.Limit {
			hits = hits[:req.Limit]
		}
		if !req.Expand {
			ids := make([]string, 0, len(hits))
			for _, hit := range hits {
				ids = append(ids, hit.ID)
			}
			writeJSON(w, http.StatusOK, ids)
			return
		}
		kind := kindFromLevel(req.Level)
		out := make([]any, 0, len(hits))
		for _, hit := range hits {
			res, err := h.Store.Get(kind, hit.ID)
			if err != nil {
				writeArchiveError(w, r, err)
				return
			}
			out = append(out, h.view(res))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (h Handler) view(res archive.Resource) any {
	updated := res.LastUpdate.UTC().Format("20060102T150405")
	switch res.Kind {
	case "patient":
		return map[string]any{
			"ID": res.ID, "IsStable": true, "LastUpdate": updated,
			"MainDicomTags": res.MainDicomTags, "Studies": res.Children, "Type": "Patient",
		}
	case "study":
		return map[string]any{
			"ID": res.ID, "IsStable": true, "LastUpdate": updated,
			"MainDicomTags": res.MainDicomTags, "PatientMainDicomTags": res.PatientMainDicomTags,
			"ParentPatient": res.ParentPatient, "Series": res.Children, "Type": "Study",
		}
	case "series":
		status := "Unknown"
		if res.ExpectedNumberOfInstances != nil {
			if res.InstanceCount >= *res.ExpectedNumberOfInstances {
				status = "Complete"
			} else {
				status = "Missing"
			}
		}
		return map[string]any{
			"ID": res.ID, "IsStable": true, "LastUpdate": updated,
			"MainDicomTags": res.MainDicomTags, "ParentStudy": res.ParentStudy,
			"Instances": res.Children, "Type": "Series", "Status": status,
			"ExpectedNumberOfInstances": res.ExpectedNumberOfInstances,
		}
	default:
		return map[string]any{
			"ID": res.ID, "FileSize": res.FileSize, "FileUuid": res.FileUUID,
			"IndexInSeries": res.IndexInSeries, "MainDicomTags": res.MainDicomTags,
			"ParentSeries": res.ParentSeries, "Type": "Instance",
		}
	}
}

func writeArchiveError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, archive.ErrNotFound):
		writeError(w, r, http.StatusNotFound, "resource not found")
	case errors.Is(err, archive.ErrMissingIdentity):
		writeError(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, archive.ErrNotDICOM):
		writeError(w, r, http.StatusBadRequest, err.Error())
	default:
		writeError(w, r, http.StatusInternalServerError, "internal error")
	}
}

func writeError(w http.ResponseWriter, r *http.Request, code int, msg string) {
	writeJSON(w, code, map[string]any{
		"HttpError":  http.StatusText(code),
		"HttpStatus": code,
		"Message":    msg,
		"Method":     r.Method,
		"Uri":        r.URL.RequestURI(),
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func expand(r *http.Request) bool {
	v := r.URL.Query().Get("expand")
	if v == "" {
		_, ok := r.URL.Query()["expand"]
		return ok
	}
	switch strings.ToLower(v) {
	case "0", "false", "no":
		return false
	default:
		return true
	}
}

func orthancType(kind string) string {
	switch kind {
	case "patient":
		return "Patient"
	case "study":
		return "Study"
	case "series":
		return "Series"
	default:
		return "Instance"
	}
}

func plural(kind string) string {
	switch kind {
	case "patient":
		return "patients"
	case "study":
		return "studies"
	case "series":
		return "series"
	default:
		return "instances"
	}
}

func kindFromLevel(level string) string {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "PATIENT":
		return "patient"
	case "STUDY":
		return "study"
	case "SERIES":
		return "series"
	default:
		return "instance"
	}
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		raw, _ := json.Marshal(t)
		return strings.Trim(string(raw), `"`)
	}
}

func parseTag(s string) (tag.Tag, error) {
	s = strings.NewReplacer("-", "", ",", "", "(", "", ")", "").Replace(s)
	return tag.Parse(s)
}

func simplifiedTags(ds *godicom.Dataset) map[string]any {
	return tagObject(ds)
}

func detailedTags(ds *godicom.Dataset) map[string]any {
	out := map[string]any{}
	if ds == nil {
		return out
	}
	for _, tg := range ds.SortedTags() {
		if skipTag(tg) {
			continue
		}
		elem, ok := ds.Get(tg)
		if !ok || elem == nil {
			continue
		}
		name, _ := tag.Keyword(tg)
		entry := map[string]any{"Name": name}
		if elem.VR == godicom.VRSQ {
			entry["Type"] = "Sequence"
			entry["Value"] = sequenceValue(ds, tg)
		} else if binaryVR(elem.VR) {
			continue
		} else {
			entry["Type"] = "String"
			value, ok := ds.GetString(tg)
			if !ok || value == "" {
				entry["Value"] = nil
			} else {
				entry["Value"] = value
			}
		}
		out[formatTag(tg)] = entry
	}
	return out
}

func tagObject(ds *godicom.Dataset) map[string]any {
	out := map[string]any{}
	if ds == nil {
		return out
	}
	for _, tg := range ds.SortedTags() {
		if skipTag(tg) {
			continue
		}
		elem, ok := ds.Get(tg)
		if !ok || elem == nil || binaryVR(elem.VR) {
			continue
		}
		name, known := tag.Keyword(tg)
		if !known {
			name = formatTag(tg)
		}
		if elem.VR == godicom.VRSQ {
			out[name] = sequenceValue(ds, tg)
			continue
		}
		value, ok := ds.GetString(tg)
		if !ok || value == "" {
			out[name] = nil
			continue
		}
		out[name] = value
	}
	return out
}

func sequenceValue(ds *godicom.Dataset, tg tag.Tag) []any {
	seq, ok := ds.GetSequence(tg)
	if !ok || seq == nil {
		return []any{}
	}
	out := make([]any, 0, len(seq.Items()))
	for _, item := range seq.Items() {
		out = append(out, tagObject(item))
	}
	return out
}

func skipTag(tg tag.Tag) bool {
	return tg == tag.PixelData || tg.Group() == 0x7FE0
}

func binaryVR(vr godicom.VR) bool {
	switch vr {
	case godicom.VROB, godicom.VROW, godicom.VROF, godicom.VROD, godicom.VROL, godicom.VROV, godicom.VRUN:
		return true
	default:
		return false
	}
}

func formatTag(tg tag.Tag) string {
	return strings.ToLower(leftPad(tg.Group()) + "," + leftPad(tg.Element()))
}

func leftPad(n int) string {
	const hexdigits = "0123456789abcdef"
	return string([]byte{hexdigits[(n>>12)&0xF], hexdigits[(n>>8)&0xF], hexdigits[(n>>4)&0xF], hexdigits[n&0xF]})
}
