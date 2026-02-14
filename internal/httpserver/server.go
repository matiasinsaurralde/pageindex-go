package httpserver

import (
	"log"
	"net/http"
	"time"

	"github.com/matiasinsaurralde/go-pageindex/internal/config"
)

// Server runs the HTTP API.
type Server struct {
	Handler        *Handler
	ListenAddr     string
	MaxUploadBytes int64
	RequestTimeout time.Duration
	MaxConcurrency int
	LogJSON        bool
	defaultOptions config.Options
}

// NewServer builds a Server with the given default options and flags.
func NewServer(defaultOptions config.Options, listenAddr string, maxUploadMB int, requestTimeout time.Duration, maxConcurrency int, logJSON bool) *Server {
	maxUploadBytes := int64(maxUploadMB) * 1024 * 1024
	if maxUploadBytes <= 0 {
		maxUploadBytes = 50 * 1024 * 1024 // 50 MiB default
	}
	var acquire func() (release func())
	if maxConcurrency > 0 {
		sem := make(chan struct{}, maxConcurrency)
		acquire = func() (release func()) {
			sem <- struct{}{}
			return func() { <-sem }
		}
	}
	h := &Handler{
		DefaultOptions: defaultOptions,
		MaxUploadBytes: maxUploadBytes,
		RequestTimeout: requestTimeout,
		acquire:        acquire,
	}
	return &Server{
		Handler:        h,
		ListenAddr:     listenAddr,
		MaxUploadBytes: maxUploadBytes,
		RequestTimeout: requestTimeout,
		MaxConcurrency: maxConcurrency,
		LogJSON:        logJSON,
		defaultOptions: defaultOptions,
	}
}

// Run starts the HTTP server and blocks until it exits.
func (s *Server) Run() error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", s.Handler.Healthz)
	mux.HandleFunc("GET /v1/readyz", s.Handler.Readyz)
	mux.HandleFunc("POST /v1/structure", s.Handler.Structure)

	withMiddleware := requestIDMiddleware(recoveryMiddleware(timeoutMiddleware(maxBodyMiddleware(mux, s.MaxUploadBytes), s.RequestTimeout)))

	log.Printf("listening on %s", s.ListenAddr)
	return http.ListenAndServe(s.ListenAddr, loggingMiddleware(withMiddleware, s.LogJSON))
}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic: %v", err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"code":"internal_error","message":"internal server error"},"request_id":""}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func timeoutMiddleware(next http.Handler, d time.Duration) http.Handler {
	if d <= 0 {
		return next
	}
	return http.TimeoutHandler(next, d, "request timeout")
}

func maxBodyMiddleware(next http.Handler, maxBytes int64) http.Handler {
	if maxBytes <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler, logJSON bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		elapsed := time.Since(start)
		if logJSON {
			log.Printf(`{"method":%q,"path":%q,"status":%d,"latency_ms":%d,"request_id":%q}`,
				r.Method, r.URL.Path, wrapped.status, elapsed.Milliseconds(), r.Header.Get("X-Request-ID"))
		} else {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, wrapped.status, elapsed)
		}
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// LoadServerConfig loads default options from path (empty = built-in defaults)
// and returns options and nil error, or zero options and error.
// LoadServerConfig loads default options from path (empty = built-in defaults).
func LoadServerConfig(configPath string) (config.Options, error) {
	return config.Load(configPath)
}
