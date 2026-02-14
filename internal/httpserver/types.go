package httpserver

import (
	"github.com/matiasinsaurralde/go-pageindex/internal/config"
	"github.com/matiasinsaurralde/go-pageindex/internal/model"
)

// StructureRequest is the JSON body for POST /v1/structure.
type StructureRequest struct {
	Filename  string          `json:"filename"`
	PDFBase64 string          `json:"pdf_base64"`
	RequestID string          `json:"request_id"`
	Options   *config.Options `json:"options,omitempty"`
}

// StructureResponse is the success response for POST /v1/structure.
type StructureResponse struct {
	RequestID        string         `json:"request_id,omitempty"`
	Result           model.Result   `json:"result"`
	EffectiveOptions config.Options `json:"effective_options"`
	TimingsMS        TimingsMS      `json:"timings_ms"`
}

// TimingsMS holds timing data in milliseconds.
type TimingsMS struct {
	Total int64 `json:"total"`
}

// ErrorResponse is the JSON error envelope.
type ErrorResponse struct {
	Error     ErrPayload `json:"error"`
	RequestID string     `json:"request_id,omitempty"`
}

// ErrPayload is the error object inside ErrorResponse.
type ErrPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// HealthResponse is the body for GET /v1/healthz.
type HealthResponse struct {
	Status string `json:"status"`
}
