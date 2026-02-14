package httpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/matiasinsaurralde/go-pageindex/internal/config"
)

func TestHandler_Healthz(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
	rec := httptest.NewRecorder()
	h.Healthz(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var body HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
}

func TestHandler_Readyz(t *testing.T) {
	t.Parallel()
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/v1/readyz", nil)
	rec := httptest.NewRecorder()
	h.Readyz(rec, req)
	// Either 200 or 503 depending on OPENAI_API_KEY in env
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 200 or 503", rec.Code)
	}
}

func TestHandler_Structure_method_not_allowed(t *testing.T) {
	t.Parallel()
	h := &Handler{DefaultOptions: config.DefaultOptions()}
	req := httptest.NewRequest(http.MethodGet, "/v1/structure", nil)
	rec := httptest.NewRecorder()
	h.Structure(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
	var errBody ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errBody.Error.Code != "method_not_allowed" {
		t.Errorf("error.code = %q", errBody.Error.Code)
	}
}

func TestHandler_Structure_unsupported_content_type(t *testing.T) {
	t.Parallel()
	h := &Handler{DefaultOptions: config.DefaultOptions()}
	req := httptest.NewRequest(http.MethodPost, "/v1/structure", bytes.NewReader(nil))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.Structure(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	var errBody ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errBody.Error.Code != "invalid_request" {
		t.Errorf("error.code = %q", errBody.Error.Code)
	}
}

func TestHandler_Structure_json_missing_filename(t *testing.T) {
	t.Parallel()
	h := &Handler{DefaultOptions: config.DefaultOptions()}
	body := []byte(`{"pdf_base64":"` + base64.StdEncoding.EncodeToString([]byte("not a pdf")) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/structure", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Structure(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	var errBody ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errBody.Error.Code != "invalid_request" {
		t.Errorf("error.code = %q", errBody.Error.Code)
	}
}

func TestHandler_Structure_json_missing_pdf_base64(t *testing.T) {
	t.Parallel()
	h := &Handler{DefaultOptions: config.DefaultOptions()}
	body := []byte(`{"filename":"doc.pdf"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/structure", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Structure(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_Structure_json_invalid_base64(t *testing.T) {
	t.Parallel()
	h := &Handler{DefaultOptions: config.DefaultOptions()}
	body := []byte(`{"filename":"doc.pdf","pdf_base64":"not-valid-base64!!"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/structure", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Structure(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandler_Structure_multipart_no_file(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("request_id", "req-1")
	_ = mw.Close()

	h := &Handler{DefaultOptions: config.DefaultOptions()}
	req := httptest.NewRequest(http.MethodPost, "/v1/structure", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.Structure(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	var errBody ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errBody.Error.Message != "file is required" {
		t.Errorf("error.message = %q", errBody.Error.Message)
	}
}

func TestHandler_Structure_multipart_invalid_options_json(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, _ := mw.CreateFormFile("file", "doc.pdf")
	_, _ = part.Write([]byte("%PDF-1.4 minimal"))
	_ = mw.WriteField("options", `{invalid json`)
	_ = mw.Close()

	h := &Handler{DefaultOptions: config.DefaultOptions()}
	req := httptest.NewRequest(http.MethodPost, "/v1/structure", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.Structure(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
