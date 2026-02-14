package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

type ClientErr struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *ClientErr) Error() string { return e.Message }

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, status int, reqID, code, msg string, details map[string]any) {
	WriteJSON(w, status, ErrorPayload{
		Error: ErrDetail{
			Code:    code,
			Message: msg,
			Details: details,
		},
		RequestID: reqID,
	})
}

func RequestID(r *http.Request, bodyID string) string {
	if bodyID != "" {
		return bodyID
	}
	if headerID := strings.TrimSpace(r.Header.Get("X-Request-ID")); headerID != "" {
		return headerID
	}
	return ""
}

func PositiveOrDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
