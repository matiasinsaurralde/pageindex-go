package pageindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadOptions_empty_path(t *testing.T) {
	t.Parallel()
	opt, err := LoadOptions("")
	if err != nil {
		t.Fatalf("LoadOptions(\"\") err = %v", err)
	}
	if opt.Model == "" {
		t.Error("LoadOptions(\"\") should return default options with non-empty Model")
	}
}

func TestLoadOptions_missing_file(t *testing.T) {
	t.Parallel()
	_, err := LoadOptions("/nonexistent/config.yaml")
	if err == nil {
		t.Fatal("LoadOptions(nonexistent) expected error")
	}
	if !strings.Contains(err.Error(), "read config") && !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error should mention read or missing file: %v", err)
	}
}

func TestLoadOptions_valid_yaml(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("model: custom-model\ntoc_check_page_num: 7\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	opt, err := LoadOptions(path)
	if err != nil {
		t.Fatalf("LoadOptions() err = %v", err)
	}
	if opt.Model != "custom-model" {
		t.Errorf("Model = %q, want custom-model", opt.Model)
	}
	if opt.TOCCheckPageNum != 7 {
		t.Errorf("TOCCheckPageNum = %d, want 7", opt.TOCCheckPageNum)
	}
}

func TestPageIndexPDFPath_empty_path(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	opt, _ := LoadOptions("")
	_, err := PageIndexPDFPath(ctx, "", opt)
	if err == nil {
		t.Fatal("PageIndexPDFPath(empty path) expected error")
	}
	if !strings.Contains(err.Error(), "pdf path is required") {
		t.Errorf("error = %v, want containing 'pdf path is required'", err)
	}
}

func TestPageIndexPDFPath_not_pdf(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	opt, _ := LoadOptions("")
	_, err := PageIndexPDFPath(ctx, "file.txt", opt)
	if err == nil {
		t.Fatal("PageIndexPDFPath(.txt) expected error")
	}
	if !strings.Contains(err.Error(), "must be a .pdf") {
		t.Errorf("error = %v, want containing 'must be a .pdf'", err)
	}
}

func TestPageIndexPDFPath_missing_file(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	opt, _ := LoadOptions("")
	_, err := PageIndexPDFPath(ctx, "/nonexistent/file.pdf", opt)
	if err == nil {
		t.Fatal("PageIndexPDFPath(missing file) expected error")
	}
	if !strings.Contains(err.Error(), "read pdf") {
		t.Errorf("error = %v, want containing 'read pdf'", err)
	}
}
