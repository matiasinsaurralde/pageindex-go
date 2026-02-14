package ingest

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/matiasinsaurralde/go-pageindex/examples/document-search/internal/api"
	"github.com/matiasinsaurralde/go-pageindex/internal/config"
)

func ParseRequest(r *http.Request) (filename string, pdf []byte, opts *config.Options, err error) {
	mediatype, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case strings.HasPrefix(mediatype, "multipart/form-data"):
		return parseMultipart(r)
	case mediatype == "application/json":
		return parseJSON(r)
	default:
		return "", nil, nil, &api.ClientErr{
			Status:  http.StatusBadRequest,
			Code:    "invalid_request",
			Message: "content-type must be multipart/form-data or application/json",
		}
	}
}

func parseMultipart(r *http.Request) (filename string, pdf []byte, opts *config.Options, err error) {
	boundary := ""
	for _, part := range strings.Split(r.Header.Get("Content-Type"), ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(strings.ToLower(part), "boundary=") {
			boundary = strings.Trim(strings.TrimPrefix(part, "boundary="), "\"")
		}
	}
	if boundary == "" {
		return "", nil, nil, &api.ClientErr{Status: http.StatusBadRequest, Code: "invalid_request", Message: "multipart boundary missing"}
	}

	reader := multipart.NewReader(r.Body, boundary)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, nil, &api.ClientErr{Status: http.StatusBadRequest, Code: "invalid_request", Message: "multipart read: " + err.Error()}
		}
		name := part.FormName()
		switch name {
		case "file":
			pdf, err = io.ReadAll(part)
			if err != nil {
				return "", nil, nil, &api.ClientErr{Status: http.StatusBadRequest, Code: "invalid_request", Message: "read file: " + err.Error()}
			}
			filename = filepath.Base(part.FileName())
		case "options":
			var o config.Options
			if err := json.NewDecoder(part).Decode(&o); err != nil {
				return "", nil, nil, &api.ClientErr{Status: http.StatusBadRequest, Code: "invalid_request", Message: "invalid options JSON: " + err.Error()}
			}
			opts = &o
		}
	}
	if filename == "" {
		filename = "document.pdf"
	}
	if len(pdf) == 0 {
		return "", nil, nil, &api.ClientErr{Status: http.StatusBadRequest, Code: "invalid_request", Message: "file is required"}
	}
	return filename, pdf, opts, nil
}

func parseJSON(r *http.Request) (filename string, pdf []byte, opts *config.Options, err error) {
	var req api.IngestRequestJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", nil, nil, &api.ClientErr{Status: http.StatusBadRequest, Code: "invalid_request", Message: "invalid JSON: " + err.Error()}
	}
	if strings.TrimSpace(req.Filename) == "" {
		return "", nil, nil, &api.ClientErr{Status: http.StatusBadRequest, Code: "invalid_request", Message: "filename is required"}
	}
	if strings.TrimSpace(req.PDFBase64) == "" {
		return "", nil, nil, &api.ClientErr{Status: http.StatusBadRequest, Code: "invalid_request", Message: "pdf_base64 is required"}
	}
	decoded, err := base64.StdEncoding.DecodeString(req.PDFBase64)
	if err != nil {
		return "", nil, nil, &api.ClientErr{Status: http.StatusBadRequest, Code: "invalid_request", Message: "invalid base64 in pdf_base64"}
	}
	return req.Filename, decoded, req.Options, nil
}

func MergeOptions(defaults config.Options, overrides *config.Options) config.Options {
	out := defaults
	if overrides == nil {
		out.Normalize()
		return out
	}
	if overrides.Model != "" {
		out.Model = overrides.Model
	}
	if overrides.TOCCheckPageNum > 0 {
		out.TOCCheckPageNum = overrides.TOCCheckPageNum
	}
	if overrides.TOCAttempts > 0 {
		out.TOCAttempts = overrides.TOCAttempts
	}
	if overrides.MaxPageNumEachNode > 0 {
		out.MaxPageNumEachNode = overrides.MaxPageNumEachNode
	}
	if overrides.MaxTokenNumEachNode > 0 {
		out.MaxTokenNumEachNode = overrides.MaxTokenNumEachNode
	}
	if overrides.IfAddNodeID != "" {
		out.IfAddNodeID = overrides.IfAddNodeID
	}
	if overrides.IfAddNodeSummary != "" {
		out.IfAddNodeSummary = overrides.IfAddNodeSummary
	}
	if overrides.IfAddDocDescription != "" {
		out.IfAddDocDescription = overrides.IfAddDocDescription
	}
	if overrides.IfAddNodeText != "" {
		out.IfAddNodeText = overrides.IfAddNodeText
	}
	out.Normalize()
	return out
}
