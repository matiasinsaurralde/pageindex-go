package util

import (
	"strings"
	"testing"
)

func TestExtractJSONString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty input",
			in:   "   ",
			want: "",
		},
		{
			name: "plain json unchanged",
			in:   `{"a":1}`,
			want: `{"a":1}`,
		},
		{
			name: "fenced json extracted",
			in:   "before\n```json\n{\"a\":1}\n```\nafter",
			want: `{"a":1}`,
		},
		{
			name: "none replaced with null",
			in:   `{"v": None}`,
			want: `{"v": null}`,
		},
		{
			name: "trailing commas removed from object and array",
			in:   `{"a": 1, "b": [1,2, ], }`,
			want: `{"a": 1, "b": [1,2]}`,
		},
		{
			name: "fence without closing marker left as is",
			in:   "```json\n{\"a\":1}",
			want: "```json\n{\"a\":1}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractJSONString(tt.in)
			if got != tt.want {
				t.Fatalf("ExtractJSONString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUnmarshalLoose(t *testing.T) {
	t.Run("parses cleaned payload", func(t *testing.T) {
		raw := "```json\n{\"items\":[{\"v\":None,},],}\n```"
		var out map[string]any
		if err := UnmarshalLoose(raw, &out); err != nil {
			t.Fatalf("UnmarshalLoose() unexpected error: %v", err)
		}
	})

	t.Run("empty content error", func(t *testing.T) {
		var out map[string]any
		err := UnmarshalLoose("   ", &out)
		if err == nil || err.Error() != "empty json content" {
			t.Fatalf("UnmarshalLoose() error = %v, want empty json content", err)
		}
	})

	t.Run("parse errors include shortened sample", func(t *testing.T) {
		raw := strings.Repeat("{", 250)
		var out map[string]any
		err := UnmarshalLoose(raw, &out)
		if err == nil {
			t.Fatal("UnmarshalLoose() expected parse error, got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "parse json:") {
			t.Fatalf("error %q does not contain parse json prefix", msg)
		}
		if !strings.Contains(msg, `sample="`) || !strings.Contains(msg, "...\"") {
			t.Fatalf("error %q does not include shortened sample", msg)
		}
	})
}
