package pdf

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gen2brain/go-fitz"

	"github.com/matiasinsaurralde/go-pageindex/internal/config"
	"github.com/matiasinsaurralde/go-pageindex/internal/model"
	"github.com/matiasinsaurralde/go-pageindex/internal/tokens"
)

func ExtractPagesWithTokensFromBytes(data []byte, modelName string) ([]model.Page, error) {
	return extractPagesWithFitz(data, modelName)
}

func ExtractPagesWithTokensFromBytesWithOptions(data []byte, opt config.Options) ([]model.Page, error) {
	return extractPagesWithFitz(data, opt.Model)
}

func ExtractPagesWithTokensFromReader(reader io.Reader, modelName string) ([]model.Page, error) {
	b, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read pdf bytes: %w", err)
	}
	return extractPagesWithFitz(b, modelName)
}

func ExtractPagesWithTokensFromReaderWithOptions(reader io.Reader, opt config.Options) ([]model.Page, error) {
	b, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read pdf bytes: %w", err)
	}
	return extractPagesWithFitz(b, opt.Model)
}

func extractPagesWithFitz(data []byte, modelName string) ([]model.Page, error) {
	tmpFile, err := os.CreateTemp("", "pageindex-fitz-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("create temp pdf: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("write temp pdf: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return nil, fmt.Errorf("close temp pdf: %w", err)
	}

	doc, err := fitz.New(tmpFile.Name())
	if err != nil {
		return nil, fmt.Errorf("open pdf: %w", err)
	}
	defer func() { _ = doc.Close() }()

	pageCount := doc.NumPage()
	pages := make([]model.Page, 0, pageCount)
	for pageIndex := 0; pageIndex < pageCount; pageIndex++ {
		text, err := doc.Text(pageIndex)
		if err != nil {
			return nil, fmt.Errorf("extract text from page %d: %w", pageIndex+1, err)
		}
		text = strings.TrimSpace(text)
		pages = append(pages, model.Page{
			Text:       text,
			TokenCount: tokens.Count(modelName, text),
		})
	}

	return pages, nil
}
