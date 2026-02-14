package httpserver

import (
	"testing"

	"github.com/matiasinsaurralde/go-pageindex/internal/config"
)

func TestMergeOptions_nil_request(t *testing.T) {
	t.Parallel()
	server := config.DefaultOptions()
	server.Model = "server-model"
	merged := MergeOptions(server, nil)
	if merged.Model != "server-model" {
		t.Errorf("merged.Model = %q, want server-model", merged.Model)
	}
	if merged.TOCCheckPageNum != 20 {
		t.Errorf("merged.TOCCheckPageNum = %d, want 20", merged.TOCCheckPageNum)
	}
}

func TestMergeOptions_request_overrides(t *testing.T) {
	t.Parallel()
	server := config.DefaultOptions()
	request := &config.Options{
		Model:         "request-model",
		TOCAttempts:   5,
		IfAddNodeText: "yes",
	}
	merged := MergeOptions(server, request)
	if merged.Model != "request-model" {
		t.Errorf("merged.Model = %q, want request-model", merged.Model)
	}
	if merged.TOCAttempts != 5 {
		t.Errorf("merged.TOCAttempts = %d, want 5", merged.TOCAttempts)
	}
	if merged.IfAddNodeText != "yes" {
		t.Errorf("merged.IfAddNodeText = %q, want yes", merged.IfAddNodeText)
	}
	// Unset in request keeps server default
	if merged.TOCCheckPageNum != 20 {
		t.Errorf("merged.TOCCheckPageNum = %d, want 20 (default)", merged.TOCCheckPageNum)
	}
}

func TestMergeOptions_request_zero_keeps_default(t *testing.T) {
	t.Parallel()
	server := config.DefaultOptions()
	request := &config.Options{
		TOCAttempts: 0, // zero means "not set"
	}
	merged := MergeOptions(server, request)
	if merged.TOCAttempts != 2 {
		t.Errorf("merged.TOCAttempts = %d, want 2 (default)", merged.TOCAttempts)
	}
}
