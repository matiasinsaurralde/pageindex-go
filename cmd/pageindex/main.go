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

	"github.com/VectifyAI/PageIndex/pkg/pageindex"
)

func main() {
	pdfPath := flag.String("pdf-path", "", "Path to PDF file")
	configPath := flag.String("config", "pageindex/config.yaml", "Path to config yaml")
	modelOverride := flag.String("model", "", "Optional model override")
	outputPath := flag.String("output", "", "Optional output JSON file path")
	flag.Parse()

	if strings.TrimSpace(*pdfPath) == "" {
		log.Fatal("--pdf-path is required")
	}
	if strings.ToLower(filepath.Ext(*pdfPath)) != ".pdf" {
		log.Fatal("--pdf-path must end with .pdf")
	}

	opts, err := pageindex.LoadOptions(*configPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}
	if strings.TrimSpace(*modelOverride) != "" {
		opts.Model = *modelOverride
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	result, err := pageindex.PageIndexPDFPath(ctx, *pdfPath, opts)
	if err != nil {
		log.Fatalf("pageindex processing failed: %v", err)
	}

	pretty, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Fatalf("failed to marshal result: %v", err)
	}

	if strings.TrimSpace(*outputPath) == "" {
		fmt.Println(string(pretty))
		return
	}

	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
		log.Fatalf("failed to create output directory: %v", err)
	}
	if err := os.WriteFile(*outputPath, pretty, 0o644); err != nil {
		log.Fatalf("failed to write output: %v", err)
	}
}
