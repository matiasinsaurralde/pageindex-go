package pdf

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/matiasinsaurralde/go-pageindex/internal/config"
)

type errReader struct{ err error }

func (e errReader) Read([]byte) (int, error) { return 0, e.err }

func TestExtractPagesWithTokensFromReader_read_error(t *testing.T) {
	t.Parallel()
	readErr := errors.New("read failed")
	_, err := ExtractPagesWithTokensFromReader(errReader{err: readErr}, "gpt-4o-2024-11-20")
	if err == nil {
		t.Fatal("expected error from failing reader")
	}
	if !errors.Is(err, readErr) {
		t.Errorf("error should wrap read error: %v", err)
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("error message should mention read: %v", err)
	}
}

func TestExtractPagesWithTokensFromBytes_invalid_pdf(t *testing.T) {
	t.Parallel()
	_, err := ExtractPagesWithTokensFromBytes([]byte("not a pdf"), "gpt-4o-2024-11-20")
	if err == nil {
		t.Fatal("expected error for non-PDF bytes")
	}
}

func TestExtractPagesWithTokensFromBytes_valid_pdf(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "testdata", "socrates.pdf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("testdata PDF not found: %v", err)
	}
	pages, err := ExtractPagesWithTokensFromBytes(data, "gpt-4o-2024-11-20")
	if err != nil {
		t.Fatalf("ExtractPagesWithTokensFromBytes: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("expected at least one page")
	}
	for i, p := range pages {
		if p.TokenCount < 0 {
			t.Errorf("page %d: TokenCount = %d", i, p.TokenCount)
		}
	}
}

func TestExtractPagesWithTokensFromReaderWithOptions_valid_pdf(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "testdata", "socrates.pdf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("testdata PDF not found: %v", err)
	}
	opt := config.DefaultOptions()
	opt.Normalize()
	pages, err := ExtractPagesWithTokensFromReaderWithOptions(&bytesReader{data: data}, opt)
	if err != nil {
		t.Fatalf("ExtractPagesWithTokensFromReaderWithOptions: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("expected at least one page")
	}
}

type bytesReader struct{ data []byte }

func (b *bytesReader) Read(p []byte) (n int, err error) {
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n = copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}
