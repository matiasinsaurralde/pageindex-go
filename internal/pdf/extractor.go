package pdf

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	pdf "github.com/ledongthuc/pdf"

	"github.com/matiasinsaurralde/go-pageindex/internal/model"
	"github.com/matiasinsaurralde/go-pageindex/internal/tokens"
)

func ExtractPagesWithTokensFromBytes(data []byte, modelName string) ([]model.Page, error) {
	r := bytes.NewReader(data)
	return ExtractPagesWithTokensFromReaderAt(r, int64(len(data)), modelName)
}

func ExtractPagesWithTokensFromReader(reader io.Reader, modelName string) ([]model.Page, error) {
	b, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read pdf bytes: %w", err)
	}
	return ExtractPagesWithTokensFromBytes(b, modelName)
}

func ExtractPagesWithTokensFromReaderAt(reader io.ReaderAt, size int64, modelName string) ([]model.Page, error) {
	doc, err := pdf.NewReader(reader, size)
	if err != nil {
		return nil, fmt.Errorf("open pdf reader: %w", err)
	}

	pageCount := doc.NumPage()
	pages := make([]model.Page, 0, pageCount)
	for pageIndex := 1; pageIndex <= pageCount; pageIndex++ {
		p := doc.Page(pageIndex)
		if p.V.IsNull() {
			pages = append(pages, model.Page{})
			continue
		}

		text, err := p.GetPlainText(nil)
		if err != nil {
			return nil, fmt.Errorf("extract text from page %d: %w", pageIndex, err)
		}
		text = strings.TrimSpace(text)
		pages = append(pages, model.Page{
			Text:       text,
			TokenCount: tokens.Count(modelName, text),
		})
	}

	return pages, nil
}
