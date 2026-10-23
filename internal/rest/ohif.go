package rest

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// OHIF is served the way the Orthanc OHIF plugin does: a built
// platform/app/dist directory, with app-config.js replaced so the viewer
// talks to this server's /dicom-web. Orthanc does not vendor OHIF as a git
// submodule; it builds a release tarball and embeds the dist.
func (h Handler) ohif(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		name := strings.TrimPrefix(r.URL.Path, "/ohif/")
		if name == "app-config.js" {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write(ohifConfig)
			return
		}
		if name == "manifest.json" {
			if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Type", "application/manifest+json")
				_, _ = w.Write(ohifManifest)
				return
			}
		}
		if name == "" || ohifMode(name) {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		r.URL.Path = "/" + name
		files.ServeHTTP(w, r)
	})
}

func ohifMode(name string) bool {
	switch name {
	case "viewer", "tmtv", "segmentation", "microscopy":
		return true
	default:
		return false
	}
}

func ohifDirReady(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !info.IsDir()
}

// ohifConfig follows Sources/app-config-system.js in orthanc-ohif, with the
// DICOMweb root pinned to this server. qidoSupportsIncludeField stays false,
// matching the Orthanc plugin, until QIDO includefield is implemented.
var ohifManifest = []byte(`{"name":"OHIF Viewer","short_name":"OHIF","start_url":"./","display":"standalone"}`)

var ohifConfig = []byte(`window.config = window.config || {};
window.config.extensions = [];
window.config.modes = [];
window.config.routerBasename = "/ohif/";
window.config.dataSources = [{
  friendlyName: "gopacs",
  namespace: "@ohif/extension-default.dataSourcesModule.dicomweb",
  sourceName: "dicomweb",
  configuration: {
    name: "gopacs",
    wadoUriRoot: "/dicom-web",
    qidoRoot: "/dicom-web",
    wadoRoot: "/dicom-web",
    qidoSupportsIncludeField: false,
    supportsReject: false,
    imageRendering: "wadors",
    thumbnailRendering: "wadors",
    enableStudyLazyLoad: true,
    supportsFuzzyMatching: false,
    supportsWildcard: true,
    staticWado: true,
    singlepart: "bulkdata,video",
    acceptHeader: ["multipart/related; type=application/octet-stream; transfer-syntax=*"],
    bulkDataURI: { enabled: true, relativeResolution: "studies" }
  }
}];
window.config.defaultDataSourceName = "dicomweb";
`)
