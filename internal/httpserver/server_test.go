package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/matiasinsaurralde/go-pageindex/internal/config"
)

func TestServer_routes(t *testing.T) {
	t.Parallel()
	opts := config.DefaultOptions()
	srv := NewServer(opts, ":0", 50, 0, 0, false)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", srv.Handler.Healthz)
	mux.HandleFunc("GET /v1/readyz", srv.Handler.Readyz)
	mux.HandleFunc("POST /v1/structure", srv.Handler.Structure)

	server := httptest.NewServer(mux)
	defer server.Close()

	t.Run("healthz", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/healthz", nil)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	})
	t.Run("readyz", func(t *testing.T) {
		req, _ := http.NewRequest("GET", server.URL+"/v1/readyz", nil)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 200 or 503", resp.StatusCode)
		}
	})
}

func TestNewServer_defaults(t *testing.T) {
	t.Parallel()
	opts := config.DefaultOptions()
	srv := NewServer(opts, ":8080", 0, 30*time.Minute, 2, false)
	if srv.Handler.MaxUploadBytes != 50*1024*1024 {
		t.Errorf("MaxUploadBytes = %d, want 50 MiB default", srv.Handler.MaxUploadBytes)
	}
	if srv.Handler.RequestTimeout != 30*time.Minute {
		t.Errorf("RequestTimeout = %v", srv.Handler.RequestTimeout)
	}
	if srv.Handler.acquire == nil {
		t.Error("acquire should be set when maxConcurrency=2")
	}
}
