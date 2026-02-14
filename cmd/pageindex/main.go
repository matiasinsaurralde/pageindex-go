package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/matiasinsaurralde/go-pageindex/internal/httpserver"
	"github.com/matiasinsaurralde/go-pageindex/pkg/pageindex"
)

func main() {
	serve := flag.Bool("serve", false, "Run HTTP server instead of CLI")
	listen := flag.String("listen", ":8080", "Listen address (server mode)")
	configPath := flag.String("config", "", "Path to config YAML (optional; omit to use defaults)")
	maxUploadMB := flag.Int("max-upload-mb", 50, "Max PDF upload size in MiB (server mode)")
	requestTimeout := flag.Duration("request-timeout", 30*time.Minute, "Per-request timeout (server mode)")
	maxConcurrency := flag.Int("max-concurrency", 2, "Max concurrent structure requests (server mode)")
	logJSON := flag.Bool("log-json", false, "Emit JSON logs (server mode)")

	pdfPath := flag.String("pdf-path", "", "Path to PDF file (CLI mode)")
	modelOverride := flag.String("model", "", "Optional model override (CLI mode)")
	outputPath := flag.String("output", "", "Optional output JSON file path (CLI mode)")
	flag.Parse()

	if *serve {
		runServer(*configPath, *listen, *maxUploadMB, *requestTimeout, *maxConcurrency, *logJSON)
		return
	}

	runCLI(*pdfPath, *configPath, *modelOverride, *outputPath)
}

func runServer(configPath, listen string, maxUploadMB int, requestTimeout time.Duration, maxConcurrency int, logJSON bool) {
	opts, err := httpserver.LoadServerConfig(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	srv := httpserver.NewServer(opts, listen, maxUploadMB, requestTimeout, maxConcurrency, logJSON)
	if err := srv.Run(); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func runCLI(pdfPath, configPath, modelOverride, outputPath string) {
	if strings.TrimSpace(pdfPath) == "" {
		log.Fatal("--pdf-path is required")
	}
	if strings.ToLower(filepath.Ext(pdfPath)) != ".pdf" {
		log.Fatal("--pdf-path must end with .pdf")
	}

	opts, err := pageindex.LoadOptions(configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if strings.TrimSpace(modelOverride) != "" {
		opts.Model = modelOverride
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	result, err := pageindex.PageIndexPDFPath(ctx, pdfPath, opts)
	if err != nil {
		log.Fatalf("pageindex processing failed: %v", err)
	}

	pretty, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Fatalf("failed to marshal result: %v", err)
	}

	if strings.TrimSpace(outputPath) == "" {
		fmt.Println(string(pretty))
		return
	}

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		log.Fatalf("failed to create output directory: %v", err)
	}
	if err := os.WriteFile(outputPath, pretty, 0o644); err != nil {
		log.Fatalf("failed to write output: %v", err)
	}
}
