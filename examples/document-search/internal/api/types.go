package api

import "github.com/matiasinsaurralde/go-pageindex/internal/config"

type ErrorPayload struct {
	Error     ErrDetail `json:"error"`
	RequestID string    `json:"request_id,omitempty"`
}

type ErrDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type IngestRequestJSON struct {
	Filename  string          `json:"filename"`
	PDFBase64 string          `json:"pdf_base64"`
	RequestID string          `json:"request_id"`
	Options   *config.Options `json:"options,omitempty"`
}

type TimingsMS struct {
	Total int64 `json:"total"`
}

type IngestResponse struct {
	RequestID        string         `json:"request_id,omitempty"`
	DocID            string         `json:"doc_id"`
	DocName          string         `json:"doc_name"`
	ChunksIndexed    int            `json:"chunks_indexed"`
	EffectiveOptions config.Options `json:"effective_options"`
	TimingsMS        TimingsMS      `json:"timings_ms"`
}

type SearchRequest struct {
	Query           string `json:"query"`
	TopKChunks      int    `json:"top_k_chunks"`
	TopNDocs        int    `json:"top_n_docs"`
	MaxChunksPerDoc int    `json:"max_chunks_per_doc"`
	RequestID       string `json:"request_id"`
}

type SearchResponse struct {
	RequestID string             `json:"request_id,omitempty"`
	Query     string             `json:"query"`
	Results   []RankedDocPayload `json:"results"`
}

type RankedDocPayload struct {
	DocID    string              `json:"doc_id"`
	DocName  string              `json:"doc_name"`
	DocScore float64             `json:"doc_score"`
	HitCount int                 `json:"hit_count"`
	Chunks   []ChunkMatchPayload `json:"chunks"`
}

type ChunkMatchPayload struct {
	ChunkID    string  `json:"chunk_id"`
	NodeID     string  `json:"node_id,omitempty"`
	ChunkScore float64 `json:"chunk_score"`
	Text       string  `json:"text"`
}

type AskRequest struct {
	Question         string  `json:"question"`
	TopKChunks       int     `json:"top_k_chunks"`
	TopNDocs         int     `json:"top_n_docs"`
	MaxContextChunks int     `json:"max_context_chunks"`
	Temperature      float32 `json:"temperature"`
	Model            string  `json:"model,omitempty"`
	RequestID        string  `json:"request_id"`
}

type AskResponse struct {
	RequestID  string              `json:"request_id,omitempty"`
	Question   string              `json:"question"`
	Answer     string              `json:"answer"`
	Citations  []CitationPayload   `json:"citations"`
	Search     AskSearchPayload    `json:"search"`
	FinishMode string              `json:"finish_reason,omitempty"`
	TimingsMS  map[string]int64    `json:"timings_ms,omitempty"`
	RawContext []ChunkMatchPayload `json:"context_chunks,omitempty"`
}

type AskSearchPayload struct {
	TopDocIDs []string `json:"top_doc_ids"`
}

type CitationPayload struct {
	DocID   string `json:"doc_id"`
	DocName string `json:"doc_name,omitempty"`
	NodeID  string `json:"node_id,omitempty"`
	ChunkID string `json:"chunk_id"`
}

type DeleteResponse struct {
	RequestID string `json:"request_id,omitempty"`
	DocID     string `json:"doc_id"`
	Deleted   bool   `json:"deleted"`
}
