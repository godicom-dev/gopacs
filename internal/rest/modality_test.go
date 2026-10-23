package rest

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gopacs/internal/archive"
)

func TestModalityEchoAndStore(t *testing.T) {
	st, err := archive.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := Handler{Store: st, System: System{Name: "gopacs", DicomAet: "GOPACS"}}.Routes()
	srv := httptest.NewServer(h)
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stored atomic.Int32
	errCh := make(chan error, 1)
	go func() {
		errCh <- ae.Serve(ctx, ln, ae.ServerConfig{
			AETitle:                  "REMOTE",
			AcceptedAbstractSyntaxes: []string{string(uid.CTImageStorage)},
			OnCStore: func(context.Context, ae.StoreRequest) uint16 {
				stored.Add(1)
				return dimse.StatusSuccess
			},
		})
	}()

	host, portText, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	put := httptest.NewRequest(http.MethodPut, "/modalities/remote", bytes.NewBufferString(fmt.Sprintf(`{"AET":"REMOTE","Host":%q,"Port":%d}`, host, port)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, put)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"AllowMove":true`)) || bytes.Contains(rec.Body.Bytes(), []byte(`"AllowGet":true`)) {
		t.Fatalf("put %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/modalities", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "[\"remote\"]\n" {
		t.Fatalf("list %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/modalities/remote/echo", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("echo %d %s", rec.Code, rec.Body.String())
	}

	up := httptest.NewRecorder()
	h.ServeHTTP(up, httptest.NewRequest(http.MethodPost, "/instances", bytes.NewReader(part10(t))))
	if up.Code != http.StatusOK {
		t.Fatalf("upload %d %s", up.Code, up.Body.String())
	}
	uploaded := readJSON(t, up.Result())
	storedID := uploaded["ParentStudy"].(string)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/modalities/remote/store", bytes.NewBufferString(`["`+storedID+`"]`)))
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"Completed":1`)) || stored.Load() != 1 {
		t.Fatalf("store %d %s stored %d", rec.Code, rec.Body.String(), stored.Load())
	}

	dup := httptest.NewRecorder()
	h.ServeHTTP(dup, httptest.NewRequest(http.MethodPut, "/modalities/other", bytes.NewBufferString(`{"AET":"REMOTE","Host":"127.0.0.1","Port":104}`)))
	if dup.Code != http.StatusConflict {
		t.Fatalf("duplicate %d %s", dup.Code, dup.Body.String())
	}

	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/app/modalities", nil))
	if page.Code != http.StatusOK || !bytes.Contains(page.Body.Bytes(), []byte("C-ECHO")) || !bytes.Contains(page.Body.Bytes(), []byte(">remote<")) {
		t.Fatalf("page %d %s", page.Code, page.Body.String())
	}

	cancel()
	<-errCh
}
