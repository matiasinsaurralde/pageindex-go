package pageindex

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/VectifyAI/PageIndex/internal/config"
	"github.com/VectifyAI/PageIndex/internal/model"
	"github.com/VectifyAI/PageIndex/internal/pipeline"
)

type Options = config.Options
type Result = model.Result

func LoadOptions(path string) (Options, error) {
	opt, err := config.Load(path)
	if err != nil {
		return Options{}, err
	}
	opt.Normalize()
	return opt, nil
}

func PageIndexPDFBytes(ctx context.Context, filename string, data []byte, opt Options) (Result, error) {
	opt.Normalize()
	if strings.TrimSpace(filename) == "" {
		filename = "document.pdf"
	}
	return pipeline.RunPDFBytes(ctx, filename, data, opt)
}

func PageIndexPDFReader(ctx context.Context, filename string, r io.Reader, opt Options) (Result, error) {
	opt.Normalize()
	if strings.TrimSpace(filename) == "" {
		filename = "document.pdf"
	}
	return pipeline.RunPDFReader(ctx, filename, r, opt)
}

// PageIndexPDFPath is a CLI adapter convenience function.
func PageIndexPDFPath(ctx context.Context, path string, opt Options) (Result, error) {
	if strings.TrimSpace(path) == "" {
		return Result{}, fmt.Errorf("pdf path is required")
	}
	if !strings.HasSuffix(strings.ToLower(path), ".pdf") {
		return Result{}, fmt.Errorf("input file must be a .pdf")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read pdf: %w", err)
	}
	return PageIndexPDFBytes(ctx, filepath.Base(path), data, opt)
}
