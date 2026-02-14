package httpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/matiasinsaurralde/go-pageindex/internal/config"
	"github.com/matiasinsaurralde/go-pageindex/pkg/pageindex"
)

func isReady() bool {
	return os.Getenv("OPENAI_API_KEY") != ""
}

// Handler holds server config for HTTP handlers.
type Handler struct {
	DefaultOptions config.Options
	MaxUploadBytes int64
	RequestTimeout time.Duration
	acquire        func() (release func()) // nil = no limit
}

// Healthz returns 200 with {"status":"ok"}.
func (h *Handler) Healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
}

// Readyz returns 200 when config and OPENAI_API_KEY are ready, else 503.
func (h *Handler) Readyz(w http.ResponseWriter, r *http.Request) {
	ready := isReady()
	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ErrorResponse{
			Error: ErrPayload{Code: "not_ready", Message: "service not ready"},
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(HealthResponse{Status: "ok"})
}

// Structure handles POST /v1/structure (multipart or JSON).
func (h *Handler) Structure(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		h.writeError(w, r, "", http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		return
	}
	reqID := requestID(r)

	if h.acquire != nil {
		release := h.acquire()
		defer release()
	}

	ctx := r.Context()
	if h.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.RequestTimeout)
		defer cancel()
	}

	contentType := r.Header.Get("Content-Type")
	mediatype, _, _ := mime.ParseMediaType(contentType)

	var filename string
	var pdfBytes []byte
	var opts *config.Options
	var bodyReqID string
	var parseErr error

	switch {
	case strings.HasPrefix(mediatype, "multipart/form-data"):
		filename, pdfBytes, opts, bodyReqID, parseErr = h.parseMultipart(r)
	case mediatype == "application/json":
		filename, pdfBytes, opts, bodyReqID, parseErr = h.parseJSON(r)
	default:
		h.writeError(w, r, reqID, http.StatusBadRequest, "invalid_request", "content-type must be multipart/form-data or application/json", nil)
		return
	}

	if bodyReqID != "" {
		reqID = bodyReqID
	}
	if parseErr != nil {
		var clientErr *clientError
		if errors.As(parseErr, &clientErr) {
			h.writeError(w, r, reqID, clientErr.status, clientErr.code, clientErr.msg, clientErr.details)
			return
		}
		h.writeError(w, r, reqID, http.StatusInternalServerError, "internal_error", parseErr.Error(), nil)
		return
	}

	if len(pdfBytes) == 0 {
		h.writeError(w, r, reqID, http.StatusBadRequest, "invalid_request", "file is required", nil)
		return
	}
	if h.MaxUploadBytes > 0 && int64(len(pdfBytes)) > h.MaxUploadBytes {
		h.writeError(w, r, reqID, http.StatusRequestEntityTooLarge, "payload_too_large", "PDF exceeds size limit", nil)
		return
	}
	if filename == "" {
		filename = "document.pdf"
	}

	merged := MergeOptions(h.DefaultOptions, opts)
	start := time.Now()
	result, err := pageindex.PageIndexPDFBytes(ctx, filename, pdfBytes, merged)
	elapsed := time.Since(start).Milliseconds()

	if err != nil {
		code, apiCode := errToStatus(err)
		h.writeError(w, r, reqID, code, apiCode, err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(StructureResponse{
		RequestID:        reqID,
		Result:           result,
		EffectiveOptions: merged,
		TimingsMS:        TimingsMS{Total: elapsed},
	})
}

type clientError struct {
	status  int
	code    string
	msg     string
	details map[string]any
}

func (e *clientError) Error() string { return e.msg }

func (h *Handler) parseMultipart(r *http.Request) (filename string, pdf []byte, opts *config.Options, reqID string, err error) {
	boundary := ""
	for _, p := range strings.Split(r.Header.Get("Content-Type"), ";") {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(strings.ToLower(p), "boundary=") {
			boundary = strings.Trim(strings.TrimPrefix(p, "boundary="), "\"")
			break
		}
	}
	if boundary == "" {
		return "", nil, nil, "", &clientError{http.StatusBadRequest, "invalid_request", "multipart boundary missing", nil}
	}
	mr := multipart.NewReader(r.Body, boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, nil, "", &clientError{http.StatusBadRequest, "invalid_request", "multipart: " + err.Error(), nil}
		}
		name := part.FormName()
		if name == "" {
			name = part.FileName()
		}
		switch name {
		case "file":
			pdf, err = io.ReadAll(part)
			if err != nil {
				return "", nil, nil, "", &clientError{http.StatusBadRequest, "invalid_request", "reading file: " + err.Error(), nil}
			}
			if filename == "" && part.FileName() != "" {
				filename = part.FileName()
			}
			if filename == "" {
				filename = "document.pdf"
			}
		case "options":
			var o config.Options
			if err := json.NewDecoder(part).Decode(&o); err != nil {
				return "", nil, nil, "", &clientError{http.StatusBadRequest, "invalid_request", "invalid options JSON: " + err.Error(), nil}
			}
			opts = &o
		case "request_id":
			b, _ := io.ReadAll(part)
			reqID = strings.TrimSpace(string(b))
		}
	}
	return filename, pdf, opts, reqID, nil
}

func (h *Handler) parseJSON(r *http.Request) (filename string, pdf []byte, opts *config.Options, reqID string, err error) {
	var req StructureRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", nil, nil, "", &clientError{http.StatusBadRequest, "invalid_request", "invalid JSON: " + err.Error(), nil}
	}
	if req.Filename == "" {
		return "", nil, nil, "", &clientError{http.StatusBadRequest, "invalid_request", "filename is required", nil}
	}
	if req.PDFBase64 == "" {
		return "", nil, nil, "", &clientError{http.StatusBadRequest, "invalid_request", "pdf_base64 is required", nil}
	}
	pdf, err = base64.StdEncoding.DecodeString(req.PDFBase64)
	if err != nil {
		return "", nil, nil, "", &clientError{http.StatusBadRequest, "invalid_request", "invalid base64 in pdf_base64", nil}
	}
	return req.Filename, pdf, req.Options, req.RequestID, nil
}

func requestID(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	return ""
}

func errToStatus(err error) (status int, code string) {
	if err == nil {
		return http.StatusOK, ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return http.StatusGatewayTimeout, "timeout"
	case strings.Contains(msg, "LLM") || strings.Contains(msg, "openai") || strings.Contains(msg, "upstream"):
		return http.StatusBadGateway, "upstream_error"
	case strings.Contains(msg, "rate") || strings.Contains(msg, "429"):
		return http.StatusTooManyRequests, "rate_limited"
	case strings.Contains(msg, "pdf") || strings.Contains(msg, "invalid") || strings.Contains(msg, "corrupt"):
		return http.StatusUnprocessableEntity, "unprocessable_document"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, reqID string, status int, code, message string, details map[string]any) {
	if reqID == "" {
		reqID = requestID(r)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{
		Error:     ErrPayload{Code: code, Message: message, Details: details},
		RequestID: reqID,
	})
}
