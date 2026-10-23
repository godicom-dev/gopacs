package rest

import (
	"archive/zip"
	"net/http"
	"os"
	"strings"
)

func (h Handler) zipArchive(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		files, err := h.Store.ArchiveFiles(kind, r.PathValue("id"))
		if err != nil {
			writeArchiveError(w, r, err)
			return
		}
		name := r.PathValue("id") + ".zip"
		if filename := r.URL.Query().Get("filename"); filename != "" && !strings.ContainsAny(filename, `/\`) {
			name = filename
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		zw := zip.NewWriter(w)
		for _, file := range files {
			raw, err := os.ReadFile(file.Path)
			if err != nil {
				_ = zw.Close()
				return
			}
			entry, err := zw.Create(file.Name)
			if err != nil {
				_ = zw.Close()
				return
			}
			if _, err := entry.Write(raw); err != nil {
				_ = zw.Close()
				return
			}
		}
		_ = zw.Close()
	}
}
