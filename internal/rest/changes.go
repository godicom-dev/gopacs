package rest

import (
	"net/http"
	"strconv"
)

func (h Handler) changes(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, last, done, err := h.Store.Changes(since, limit)
	if err != nil {
		writeArchiveError(w, r, err)
		return
	}
	changes := make([]map[string]any, 0, len(items))
	for _, item := range items {
		changes = append(changes, map[string]any{
			"ChangeType": item.Type, "Date": item.Date, "ID": item.ID,
			"Path": resourcePath(item.ResourceType, item.ID), "ResourceType": item.ResourceType, "Seq": item.Seq,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"Changes": changes, "Done": done, "Last": last})
}

func (h Handler) clearChanges(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.ClearChanges(); err != nil {
		writeArchiveError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func resourcePath(resourceType, id string) string {
	switch resourceType {
	case "Patient":
		return "/patients/" + id
	case "Study":
		return "/studies/" + id
	case "Series":
		return "/series/" + id
	default:
		return "/instances/" + id
	}
}
