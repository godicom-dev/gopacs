// Package rest serves the Orthanc-compatible routes gopacs implements.
package rest

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/godicom-dev/gonetdicom/dicomweb"
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
	// OHIFDir is a built OHIF platform/app/dist. Empty or missing index.html leaves /ohif/ unregistered.
	OHIFDir string
}

// Routes returns the Orthanc-shaped routes this server implements.
func (h Handler) Routes() http.Handler {
	max := h.MaxUpload
	if max <= 0 {
		max = 256 << 20
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /app/patients", h.uiPatients)
	mux.HandleFunc("GET /app/studies", h.uiStudies)
	mux.HandleFunc("GET /app/patients/{id}", h.uiPatient)
	mux.HandleFunc("GET /app/studies/{id}", h.uiStudy)
	mux.HandleFunc("GET /app/series/{id}", h.uiSeries)
	mux.HandleFunc("GET /app/instances/{id}", h.uiInstance)
	mux.HandleFunc("GET /app/upload", h.uiUpload)
	mux.HandleFunc("GET /app/lookup", h.uiLookup)
	mux.HandleFunc("GET /app/modalities", h.uiModalities)
	mux.HandleFunc("POST /app/modalities", h.uiPutModality)
	mux.HandleFunc("POST /app/modalities/{name}/echo", h.uiEchoModality)
	mux.HandleFunc("POST /app/modalities/{name}/delete", h.uiDeleteModality)
	mux.HandleFunc("POST /app/patients/{id}/send", h.uiSend("patient"))
	mux.HandleFunc("POST /app/studies/{id}/send", h.uiSend("study"))
	mux.HandleFunc("POST /app/series/{id}/send", h.uiSend("series"))
	mux.HandleFunc("POST /app/instances/{id}/send", h.uiSend("instance"))
	mux.HandleFunc("GET /app/explorer.css", appAsset("explorer.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /app/explorer.js", appAsset("explorer.js", "text/javascript; charset=utf-8"))
	if ohifDirReady(h.OHIFDir) {
		mux.HandleFunc("GET /ohif", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ohif/", http.StatusFound)
		})
		mux.Handle("GET /ohif/", h.ohif(h.OHIFDir))
	}
	mux.HandleFunc("GET /system", h.system)
	mux.HandleFunc("GET /statistics", h.statistics)
	mux.HandleFunc("GET /changes", h.changes)
	mux.HandleFunc("DELETE /changes", h.clearChanges)
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

	mux.HandleFunc("GET /patients/{id}/statistics", h.resourceStatistics("patient"))
	mux.HandleFunc("GET /studies/{id}/statistics", h.resourceStatistics("study"))
	mux.HandleFunc("GET /series/{id}/statistics", h.resourceStatistics("series"))
	mux.HandleFunc("GET /instances/{id}/statistics", h.resourceStatistics("instance"))

	mux.HandleFunc("GET /patients/{id}/archive", h.zipArchive("patient"))
	mux.HandleFunc("GET /studies/{id}/archive", h.zipArchive("study"))
	mux.HandleFunc("GET /series/{id}/archive", h.zipArchive("series"))
	mux.HandleFunc("GET /instances/{id}/archive", h.zipArchive("instance"))

	mux.HandleFunc("GET /instances/{id}/file", h.file)
	mux.HandleFunc("GET /instances/{id}/simplified-tags", h.simplified)
	mux.HandleFunc("GET /instances/{id}/tags", h.tags)
	mux.HandleFunc("GET /instances/{id}/content/{tag}", h.content)

	mux.HandleFunc("POST /instances", h.upload(max))
	mux.HandleFunc("POST /tools/lookup", h.lookup(max))
	mux.HandleFunc("POST /tools/find", h.find(max))
	mux.HandleFunc("GET /modalities", h.modalities)
	mux.HandleFunc("PUT /modalities/{name}", h.putModality)
	mux.HandleFunc("GET /modalities/{name}", h.getModality)
	mux.HandleFunc("DELETE /modalities/{name}", h.deleteModality)
	mux.HandleFunc("POST /modalities/{name}/echo", h.echoModality)
	mux.HandleFunc("POST /modalities/{name}/store", h.storeModality)
	mux.Handle("/dicom-web/", dicomweb.Handler(webStore{store: h.Store, overwrite: h.Overwrite}, "/dicom-web", dicomweb.WithMaxRequestBytes(max)))
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
	case errors.Is(err, archive.ErrInvalidModality):
		writeError(w, r, http.StatusBadRequest, err.Error())
	case errors.Is(err, archive.ErrDuplicateAET):
		writeError(w, r, http.StatusConflict, err.Error())
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
