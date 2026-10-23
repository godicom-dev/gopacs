package rest

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed ui
var uiFiles embed.FS

var pageTmpl = template.Must(template.ParseFS(uiFiles, "ui/pages.tmpl"))

func appAsset(name, contentType string) http.HandlerFunc {
	body, err := uiFiles.ReadFile("ui/" + name)
	if err != nil {
		panic(err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}

func (h Handler) home(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/app/patients", http.StatusFound)
}
