package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/matiasinsaurralde/go-pageindex/examples/document-search/internal/api"
	"github.com/matiasinsaurralde/go-pageindex/examples/document-search/internal/ingest"
	"github.com/matiasinsaurralde/go-pageindex/examples/document-search/internal/rag"
	"github.com/matiasinsaurralde/go-pageindex/examples/document-search/internal/search"
	"github.com/matiasinsaurralde/go-pageindex/examples/document-search/internal/store"
	"github.com/matiasinsaurralde/go-pageindex/internal/config"
	"github.com/matiasinsaurralde/go-pageindex/pkg/pageindex"
	"github.com/philippgille/chromem-go"
	openai "github.com/sashabaranov/go-openai"
)

type Server struct {
	listenAddr       string
	httpServer       *http.Server
	db               *chromem.DB
	snapshotPath     string
	snapshotCompress bool
	collection       *chromem.Collection
	defaultOptions   config.Options
	chatModel        string
	enableTreeCache  bool
	logJSON          bool
	catalog          *store.Catalog
	searcher         *search.Service
}

func NewFromEnv() (*Server, error) {
	_ = godotenv.Load()

	openAIKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if openAIKey == "" {
		return nil, errors.New("missing OPENAI_API_KEY (env or .env)")
	}

	defaultOptions, err := pageindex.LoadOptions("")
	if err != nil {
		return nil, err
	}

	db := chromem.NewDB()
	snapshotPath := strings.TrimSpace(os.Getenv("DOCSEARCH_SNAPSHOT_PATH"))
	if snapshotPath == "" {
		snapshotPath = ".docsearch/chromem.gob.gz"
	}
	snapshotCompress := !strings.EqualFold(strings.TrimSpace(os.Getenv("DOCSEARCH_SNAPSHOT_COMPRESS")), "false")
	if err := importSnapshotIfPresent(db, snapshotPath); err != nil {
		return nil, err
	}
	collection, err := db.GetOrCreateCollection("pageindex-doc-search", nil, nil)
	if err != nil {
		return nil, err
	}

	listenAddr := strings.TrimSpace(os.Getenv("DOCSEARCH_LISTEN"))
	if listenAddr == "" {
		listenAddr = ":8090"
	}
	chatModel := strings.TrimSpace(os.Getenv("DOCSEARCH_CHAT_MODEL"))
	if chatModel == "" {
		chatModel = "gpt-4o-mini"
	}

	return &Server{
		listenAddr:       listenAddr,
		db:               db,
		snapshotPath:     snapshotPath,
		snapshotCompress: snapshotCompress,
		collection:       collection,
		defaultOptions:   defaultOptions,
		chatModel:        chatModel,
		enableTreeCache:  strings.EqualFold(strings.TrimSpace(os.Getenv("DOCSEARCH_ENABLE_TREE_CACHE")), "true"),
		logJSON:          strings.EqualFold(strings.TrimSpace(os.Getenv("DOCSEARCH_LOG_JSON")), "true"),
		catalog:          store.NewCatalog(),
		searcher:         &search.Service{Collection: collection},
	}, nil
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", s.healthz)
	mux.HandleFunc("GET /v1/readyz", s.readyz)
	mux.HandleFunc("POST /v1/documents", s.ingest)
	mux.HandleFunc("DELETE /v1/documents/{doc_id}", s.deleteDocument)
	mux.HandleFunc("POST /v1/search", s.search)
	mux.HandleFunc("POST /v1/ask", s.ask)

	allowedOrigins := parseAllowedOrigins(os.Getenv("DOCSEARCH_CORS_ALLOWED_ORIGINS"))
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"http://localhost:5173"}
	}
	handler := s.withCORS(mux, allowedOrigins)

	s.httpServer = &http.Server{
		Addr:    s.listenAddr,
		Handler: s.withLogging(handler),
	}
	log.Printf("document-search server listening on %s", s.listenAddr)
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		err := <-errCh
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return s.SaveSnapshot()
	}
}

func (s *Server) SaveSnapshot() error {
	if s.db == nil {
		return nil
	}
	path := strings.TrimSpace(s.snapshotPath)
	if path == "" {
		return errors.New("snapshot path is empty")
	}
	parentDir := filepath.Dir(path)
	if parentDir != "" && parentDir != "." {
		if err := os.MkdirAll(parentDir, 0o700); err != nil {
			return err
		}
	}
	if err := s.db.ExportToFile(path, s.snapshotCompress, ""); err != nil {
		return err
	}
	log.Printf("snapshot saved: %s", path)
	return nil
}

func importSnapshotIfPresent(db *chromem.DB, snapshotPath string) error {
	if strings.TrimSpace(snapshotPath) == "" {
		return errors.New("snapshot path is empty")
	}
	_, err := os.Stat(snapshotPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := db.ImportFromFile(snapshotPath, ""); err != nil {
		return err
	}
	log.Printf("snapshot loaded: %s", snapshotPath)
	return nil
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		elapsed := time.Since(start).Milliseconds()
		if s.logJSON {
			log.Printf(`{"method":%q,"path":%q,"status":%d,"latency_ms":%d}`, r.Method, r.URL.Path, wrapped.status, elapsed)
			return
		}
		log.Printf("%s %s %d %dms", r.Method, r.URL.Path, wrapped.status, elapsed)
	})
}

func parseAllowedOrigins(raw string) []string {
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		value := strings.TrimSpace(p)
		if value == "" {
			continue
		}
		origins = append(origins, value)
	}
	return origins
}

func (s *Server) withCORS(next http.Handler, allowedOrigins []string) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" {
			if _, ok := allowed[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, _ *http.Request) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		api.WriteError(w, http.StatusServiceUnavailable, "not_ready", "missing OPENAI_API_KEY", nil)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	filename, pdf, opts, err := ingest.ParseRequest(r)
	if err != nil {
		var ce *api.ClientErr
		if errors.As(err, &ce) {
			api.WriteError(w, ce.Status, ce.Code, ce.Message, ce.Details)
			return
		}
		api.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}

	merged := ingest.MergeOptions(s.defaultOptions, opts)
	merged.IfAddNodeText = "yes"
	merged.Normalize()

	start := time.Now()
	result, err := pageindex.PageIndexPDFBytes(r.Context(), filename, pdf, merged)
	if err != nil {
		api.WriteError(w, http.StatusBadGateway, "ingest_failed", err.Error(), nil)
		return
	}

	docID := ingest.NewDocID()
	chunks := ingest.ExtractChunks(docID, result.DocName, result.Structure)
	if len(chunks) == 0 {
		api.WriteError(w, http.StatusUnprocessableEntity, "ingest_failed", "no searchable chunks extracted from document", nil)
		return
	}

	chromemDocs := ingest.ToChromemDocs(chunks)
	if err := s.collection.AddDocuments(r.Context(), chromemDocs, max(1, runtime.NumCPU()/2)); err != nil {
		api.WriteError(w, http.StatusBadGateway, "vector_index_error", err.Error(), nil)
		return
	}

	record := store.DocRecord{
		DocID:      docID,
		DocName:    result.DocName,
		ChunkCount: len(chunks),
		IndexedAt:  time.Now().UTC(),
	}
	if s.enableTreeCache {
		cached := result
		record.CachedTree = &cached
	}
	s.catalog.Put(record)

	api.WriteJSON(w, http.StatusCreated, api.IngestResponse{
		DocID:            docID,
		DocName:          result.DocName,
		ChunksIndexed:    len(chunks),
		EffectiveOptions: merged,
		TimingsMS: api.TimingsMS{
			Total: time.Since(start).Milliseconds(),
		},
	})
}

func (s *Server) deleteDocument(w http.ResponseWriter, r *http.Request) {
	docID := strings.TrimSpace(r.PathValue("doc_id"))
	if docID == "" {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "doc_id is required", nil)
		return
	}

	if err := s.collection.Delete(r.Context(), map[string]string{"doc_id": docID}, nil); err != nil {
		api.WriteError(w, http.StatusBadGateway, "vector_index_error", err.Error(), nil)
		return
	}

	existed := s.catalog.Delete(docID)
	api.WriteJSON(w, http.StatusOK, api.DeleteResponse{
		DocID:   docID,
		Deleted: existed,
	})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	var req api.SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid JSON: "+err.Error(), nil)
		return
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "query is required", nil)
		return
	}

	results, err := s.searcher.Query(
		r.Context(),
		req.Query,
		api.PositiveOrDefault(req.TopKChunks, 40),
		api.PositiveOrDefault(req.TopNDocs, 5),
		api.PositiveOrDefault(req.MaxChunksPerDoc, 4),
	)
	if err != nil {
		api.WriteError(w, http.StatusBadGateway, "search_failed", err.Error(), nil)
		return
	}

	api.WriteJSON(w, http.StatusOK, api.SearchResponse{
		Query:   req.Query,
		Results: results,
	})
}

func (s *Server) ask(w http.ResponseWriter, r *http.Request) {
	var req api.AskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "invalid JSON: "+err.Error(), nil)
		return
	}
	req.Question = strings.TrimSpace(req.Question)
	if req.Question == "" {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "question is required", nil)
		return
	}

	topKChunks := api.PositiveOrDefault(req.TopKChunks, 40)
	topNDocs := api.PositiveOrDefault(req.TopNDocs, 3)
	maxContextChunks := api.PositiveOrDefault(req.MaxContextChunks, 8)

	startSearch := time.Now()
	rankedDocs, err := s.searcher.Query(r.Context(), req.Question, topKChunks, topNDocs, maxContextChunks)
	if err != nil {
		api.WriteError(w, http.StatusBadGateway, "search_failed", err.Error(), nil)
		return
	}
	searchElapsed := time.Since(startSearch).Milliseconds()

	contextChunks := rag.FlattenContextChunks(rankedDocs, maxContextChunks)
	if len(contextChunks) == 0 {
		api.WriteError(w, http.StatusUnprocessableEntity, "insufficient_context", "no context chunks found for question", nil)
		return
	}

	prompt := rag.BuildPrompt(req.Question, contextChunks)
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		modelName = s.chatModel
	}
	temp := req.Temperature
	if temp <= 0 {
		temp = 0.2
	}

	startLLM := time.Now()
	answer, finishReason, err := s.chatWithSystem(r.Context(), modelName, temp, prompt)
	if err != nil {
		api.WriteError(w, http.StatusBadGateway, "llm_error", err.Error(), nil)
		return
	}
	llmElapsed := time.Since(startLLM).Milliseconds()

	topDocIDs := make([]string, 0, len(rankedDocs))
	for _, d := range rankedDocs {
		topDocIDs = append(topDocIDs, d.DocID)
	}

	citations := make([]api.CitationPayload, 0, len(contextChunks))
	for _, ch := range contextChunks {
		citations = append(citations, api.CitationPayload{
			DocID:   ch.DocID,
			DocName: ch.DocName,
			NodeID:  ch.NodeID,
			ChunkID: ch.ChunkID,
		})
	}
	chunkScoreByID := make(map[string]float64, len(contextChunks))
	for _, doc := range rankedDocs {
		for _, ch := range doc.Chunks {
			if _, ok := chunkScoreByID[ch.ChunkID]; ok {
				continue
			}
			chunkScoreByID[ch.ChunkID] = ch.ChunkScore
		}
	}
	rawContext := make([]api.ChunkMatchPayload, 0, len(contextChunks))
	for _, ch := range contextChunks {
		rawContext = append(rawContext, api.ChunkMatchPayload{
			DocID:      ch.DocID,
			DocName:    ch.DocName,
			ChunkID:    ch.ChunkID,
			NodeID:     ch.NodeID,
			ChunkScore: chunkScoreByID[ch.ChunkID],
			Text:       ch.Text,
		})
	}

	api.WriteJSON(w, http.StatusOK, api.AskResponse{
		Question:   req.Question,
		Answer:     strings.TrimSpace(answer),
		Citations:  citations,
		Search:     api.AskSearchPayload{TopDocIDs: topDocIDs},
		FinishMode: finishReason,
		RawContext: rawContext,
		TimingsMS: map[string]int64{
			"search": searchElapsed,
			"llm":    llmElapsed,
			"total":  searchElapsed + llmElapsed,
		},
	})
}

func (s *Server) chatWithSystem(ctx context.Context, modelName string, temperature float32, prompt string) (string, string, error) {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return "", "", errors.New("missing OPENAI_API_KEY")
	}
	client := openai.NewClient(apiKey)
	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: modelName,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleSystem,
				Content: "You answer questions only from the provided context snippets. " +
					"If context is insufficient, say so explicitly. Cite supporting snippets as [doc_id:chunk_id].",
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		Temperature: temperature,
	})
	if err != nil {
		return "", "", err
	}
	if len(resp.Choices) == 0 {
		return "", "", errors.New("no choices returned by model")
	}
	return resp.Choices[0].Message.Content, string(resp.Choices[0].FinishReason), nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
