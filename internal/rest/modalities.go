package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/godicom-dev/gopacs/internal/archive"
	"github.com/godicom-dev/gopacs/internal/dimse"
)

func (h Handler) modalities(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.Modalities()
	if err != nil {
		writeArchiveError(w, r, err)
		return
	}
	if !expand(r) {
		names := make([]string, len(list))
		for i, m := range list {
			names[i] = m.Name
		}
		writeJSON(w, http.StatusOK, names)
		return
	}
	out := make(map[string]modalityJSON, len(list))
	for _, m := range list {
		out[m.Name] = modalityView(m)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h Handler) putModality(w http.ResponseWriter, r *http.Request) {
	var body archive.Modality
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "modality JSON is invalid")
		return
	}
	body.Name = r.PathValue("name")
	if err := h.Store.PutModality(body); err != nil {
		writeArchiveError(w, r, err)
		return
	}
	got, err := h.Store.Modality(body.Name)
	if err != nil {
		writeArchiveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, modalityView(got))
}

func (h Handler) getModality(w http.ResponseWriter, r *http.Request) {
	m, err := h.Store.Modality(r.PathValue("name"))
	if err != nil {
		writeArchiveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, modalityView(m))
}

func (h Handler) deleteModality(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteModality(r.PathValue("name")); err != nil {
		writeArchiveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h Handler) echoModality(w http.ResponseWriter, r *http.Request) {
	m, err := h.Store.Modality(r.PathValue("name"))
	if err != nil {
		writeArchiveError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := dimse.Echo(ctx, h.callingAE(), m); err != nil {
		writeError(w, r, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h Handler) storeModality(w http.ResponseWriter, r *http.Request) {
	m, err := h.Store.Modality(r.PathValue("name"))
	if err != nil {
		writeArchiveError(w, r, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "cannot read body")
		return
	}
	ids, err := storeResourceIDs(body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	result, err := h.sendTo(r.Context(), m, ids)
	if err != nil {
		if errors.Is(err, archive.ErrNotFound) || errors.Is(err, archive.ErrInvalidModality) || errors.Is(err, archive.ErrDuplicateAET) {
			writeArchiveError(w, r, err)
			return
		}
		writeError(w, r, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"Completed": result.Completed,
		"Failed":    result.Failed,
	})
}

func (h Handler) sendTo(ctx context.Context, m archive.Modality, ids []string) (dimse.SendResult, error) {
	var part10s [][]byte
	for _, id := range ids {
		files, err := h.part10s(id)
		if err != nil {
			return dimse.SendResult{}, err
		}
		part10s = append(part10s, files...)
	}
	return dimse.Send(ctx, h.callingAE(), m, part10s)
}

func (h Handler) part10s(id string) ([][]byte, error) {
	hits, err := h.Store.Lookup(id)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, archive.ErrNotFound
	}
	files, err := h.Store.ArchiveFiles(hits[0].Kind, hits[0].ID)
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(files))
	for _, file := range files {
		raw, err := os.ReadFile(file.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

func (h Handler) callingAE() string {
	if h.System.DicomAet != "" {
		return h.System.DicomAet
	}
	return "GOPACS"
}

type modalityJSON struct {
	AET        string `json:"AET"`
	Host       string `json:"Host"`
	Port       int    `json:"Port"`
	AllowEcho  bool   `json:"AllowEcho"`
	AllowStore bool   `json:"AllowStore"`
	AllowFind  bool   `json:"AllowFind"`
	AllowMove  bool   `json:"AllowMove"`
	AllowGet   bool   `json:"AllowGet"`
}

func modalityView(m archive.Modality) modalityJSON {
	return modalityJSON{
		AET: m.AET, Host: m.Host, Port: m.Port,
		AllowEcho: true, AllowStore: true, AllowFind: true, AllowMove: true,
	}
}

func (h Handler) uiModalities(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.Modalities()
	if err != nil {
		h.uiFail(w, err)
		return
	}
	page := uiPage{Nav: "modalities", Empty: "No modalities"}
	page.Modalities = make([]uiModality, len(list))
	for i, m := range list {
		page.Modalities[i] = uiModality{Name: m.Name, AET: m.AET, Host: m.Host, Port: m.Port}
	}
	h.render(w, http.StatusOK, "modalities", page)
}

func (h Handler) uiPutModality(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.render(w, http.StatusBadRequest, "message", uiPage{Title: "Modality", Message: "invalid form", MessageClass: "error"})
		return
	}
	port, err := strconv.Atoi(r.PostFormValue("port"))
	if err != nil {
		h.render(w, http.StatusBadRequest, "message", uiPage{Title: "Modality", Message: "port must be a number", MessageClass: "error"})
		return
	}
	err = h.Store.PutModality(archive.Modality{
		Name: r.PostFormValue("name"),
		AET:  r.PostFormValue("aet"),
		Host: r.PostFormValue("host"),
		Port: port,
	})
	if err != nil {
		h.render(w, http.StatusBadRequest, "message", uiPage{Title: "Modality", Message: err.Error(), MessageClass: "error"})
		return
	}
	http.Redirect(w, r, "/app/modalities", http.StatusSeeOther)
}

func (h Handler) uiEchoModality(w http.ResponseWriter, r *http.Request) {
	m, err := h.Store.Modality(r.PathValue("name"))
	if err != nil {
		h.uiFail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := dimse.Echo(ctx, h.callingAE(), m); err != nil {
		h.render(w, http.StatusBadGateway, "message", uiPage{Title: "C-ECHO failed", Message: err.Error(), MessageClass: "error"})
		return
	}
	h.render(w, http.StatusOK, "message", uiPage{Title: "C-ECHO", Message: m.Name + " answered."})
}

func (h Handler) uiDeleteModality(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteModality(r.PathValue("name")); err != nil {
		h.uiFail(w, err)
		return
	}
	http.Redirect(w, r, "/app/modalities", http.StatusSeeOther)
}

func (h Handler) uiSend(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			h.render(w, http.StatusBadRequest, "message", uiPage{Title: "Send", Message: "invalid form", MessageClass: "error"})
			return
		}
		m, err := h.Store.Modality(r.PostFormValue("modality"))
		if err != nil {
			h.uiFail(w, err)
			return
		}
		result, err := h.sendTo(r.Context(), m, []string{r.PathValue("id")})
		if err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, archive.ErrNotFound) {
				code = http.StatusNotFound
			}
			h.render(w, code, "message", uiPage{Title: "Send failed", Message: err.Error(), MessageClass: "error"})
			return
		}
		h.render(w, http.StatusOK, "message", uiPage{
			Title:   "Sent",
			Message: fmt.Sprintf("Sent %d instance(s) of this %s to %s. %d failed.", result.Completed, kind, m.Name, result.Failed),
		})
	}
}

func storeResourceIDs(body []byte) ([]string, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, errors.New("missing resource id")
	}
	switch body[0] {
	case '"':
		var id string
		if err := json.Unmarshal(body, &id); err != nil {
			return nil, errors.New("modality store body is invalid")
		}
		if id == "" {
			return nil, errors.New("missing resource id")
		}
		return []string{id}, nil
	case '[':
		var ids []string
		if err := json.Unmarshal(body, &ids); err != nil || len(ids) == 0 {
			return nil, errors.New("modality store body is invalid")
		}
		return ids, nil
	case '{':
		var doc struct {
			Resources []string `json:"Resources"`
		}
		if err := json.Unmarshal(body, &doc); err != nil || len(doc.Resources) == 0 {
			return nil, errors.New("modality store body is invalid")
		}
		return doc.Resources, nil
	default:
		return nil, errors.New("modality store body is invalid")
	}
}
