package util

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	trailingCommaRE = regexp.MustCompile(`,\s*([}\]])`)
)

func ExtractJSONString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}

	if start := strings.Index(raw, "```json"); start != -1 {
		start += len("```json")
		if end := strings.Index(raw[start:], "```"); end != -1 {
			raw = strings.TrimSpace(raw[start : start+end])
		}
	}

	if strings.Contains(raw, "None") {
		raw = strings.ReplaceAll(raw, "None", "null")
	}
	if strings.Contains(raw, ",") {
		raw = trailingCommaRE.ReplaceAllString(raw, "$1")
	}
	return strings.TrimSpace(raw)
}

func UnmarshalLoose(raw string, out any) error {
	clean := ExtractJSONString(raw)
	if clean == "" {
		return fmt.Errorf("empty json content")
	}
	if err := json.Unmarshal([]byte(clean), out); err != nil {
		return fmt.Errorf("parse json: %w; sample=%q", err, shorten(clean, 200))
	}
	return nil
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
