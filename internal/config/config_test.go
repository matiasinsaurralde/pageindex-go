package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultOptions(t *testing.T) {
	t.Parallel()
	o := DefaultOptions()
	if o.Model != "gpt-4o-2024-11-20" {
		t.Errorf("Model = %q, want gpt-4o-2024-11-20", o.Model)
	}
	if o.TOCCheckPageNum != 20 {
		t.Errorf("TOCCheckPageNum = %d, want 20", o.TOCCheckPageNum)
	}
	if o.TOCAttempts != 2 {
		t.Errorf("TOCAttempts = %d, want 2", o.TOCAttempts)
	}
	if o.MaxPageNumEachNode != 10 {
		t.Errorf("MaxPageNumEachNode = %d, want 10", o.MaxPageNumEachNode)
	}
	if o.MaxTokenNumEachNode != 20000 {
		t.Errorf("MaxTokenNumEachNode = %d, want 20000", o.MaxTokenNumEachNode)
	}
	if o.IfAddNodeID != "yes" || o.IfAddNodeSummary != "yes" {
		t.Errorf("IfAddNodeID/IfAddNodeSummary want yes")
	}
	if o.IfAddDocDescription != "no" || o.IfAddNodeText != "no" {
		t.Errorf("IfAddDocDescription/IfAddNodeText want no")
	}
}

func TestOptions_Normalize_empty_model(t *testing.T) {
	t.Parallel()
	o := Options{Model: ""}
	o.Normalize()
	if o.Model != "gpt-4o-2024-11-20" {
		t.Errorf("Model after Normalize = %q, want gpt-4o-2024-11-20", o.Model)
	}
}

func TestOptions_Normalize_zero_or_negative_int_fields(t *testing.T) {
	t.Parallel()
	o := Options{
		TOCCheckPageNum:     0,
		TOCAttempts:         0,
		MaxPageNumEachNode:  0,
		MaxTokenNumEachNode: 0,
	}
	o.Normalize()
	if o.TOCCheckPageNum != 20 {
		t.Errorf("TOCCheckPageNum = %d, want 20", o.TOCCheckPageNum)
	}
	if o.TOCAttempts != 1 {
		t.Errorf("TOCAttempts = %d, want 1", o.TOCAttempts)
	}
	if o.MaxPageNumEachNode != 10 {
		t.Errorf("MaxPageNumEachNode = %d, want 10", o.MaxPageNumEachNode)
	}
	if o.MaxTokenNumEachNode != 20000 {
		t.Errorf("MaxTokenNumEachNode = %d, want 20000", o.MaxTokenNumEachNode)
	}
}

func TestOptions_Normalize_if_add_yes_no(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		opt      Options
		wantID   string
		wantSum  string
		wantDesc string
		wantText string
	}{
		{"yes no preserved", Options{IfAddNodeID: "yes", IfAddNodeSummary: "no", IfAddDocDescription: "no", IfAddNodeText: "yes"}, "yes", "no", "no", "yes"},
		{"empty fallback", Options{}, "yes", "yes", "no", "no"},
		{"garbage fallback", Options{IfAddNodeID: "x", IfAddNodeSummary: "y", IfAddDocDescription: "z", IfAddNodeText: "a"}, "yes", "yes", "no", "no"},
		{"case fold", Options{IfAddNodeID: "YES", IfAddNodeSummary: "No"}, "yes", "no", "no", "no"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.opt.Normalize()
			if tt.opt.IfAddNodeID != tt.wantID || tt.opt.IfAddNodeSummary != tt.wantSum ||
				tt.opt.IfAddDocDescription != tt.wantDesc || tt.opt.IfAddNodeText != tt.wantText {
				t.Errorf("got IfAddNodeID=%q IfAddNodeSummary=%q IfAddDocDescription=%q IfAddNodeText=%q",
					tt.opt.IfAddNodeID, tt.opt.IfAddNodeSummary, tt.opt.IfAddDocDescription, tt.opt.IfAddNodeText)
			}
		})
	}
}

func TestNormalizeYesNo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value    string
		fallback string
		want     string
	}{
		{"", "yes", "yes"},
		{"", "no", "no"},
		{"yes", "no", "yes"},
		{"no", "yes", "no"},
		{"YES", "no", "yes"},
		{"  no  ", "yes", "no"},
		{"other", "yes", "yes"},
		{"other", "no", "no"},
	}
	for _, tt := range tests {
		got := normalizeYesNo(tt.value, tt.fallback)
		if got != tt.want {
			t.Errorf("normalizeYesNo(%q, %q) = %q, want %q", tt.value, tt.fallback, got, tt.want)
		}
	}
}

func TestIsYes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		want  bool
	}{
		{"yes", true},
		{"YES", true},
		{"Yes", true},
		{"no", false},
		{"", false},
		{"  yes  ", true},
	}
	for _, tt := range tests {
		got := IsYes(tt.value)
		if got != tt.want {
			t.Errorf("IsYes(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestLoad_empty_path(t *testing.T) {
	t.Parallel()
	opt, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\") err = %v", err)
	}
	if opt.Model != DefaultOptions().Model {
		t.Errorf("Load(\"\") should return defaults")
	}
}

func TestLoad_invalid_yaml(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("not: valid: yaml: ["), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load(invalid yaml) expected error")
	}
}

func TestLoad_valid_yaml(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
model: "custom-model"
toc_check_page_num: 5
if_add_node_id: "no"
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	opt, err := Load(path)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if opt.Model != "custom-model" {
		t.Errorf("Model = %q, want custom-model", opt.Model)
	}
	if opt.TOCCheckPageNum != 5 {
		t.Errorf("TOCCheckPageNum = %d, want 5", opt.TOCCheckPageNum)
	}
	if opt.IfAddNodeID != "no" {
		t.Errorf("IfAddNodeID = %q, want no", opt.IfAddNodeID)
	}
	// Unspecified should be default
	if opt.MaxPageNumEachNode != 10 {
		t.Errorf("MaxPageNumEachNode = %d, want 10", opt.MaxPageNumEachNode)
	}
}
