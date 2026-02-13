package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Options struct {
	Model               string `yaml:"model" json:"model"`
	TOCCheckPageNum     int    `yaml:"toc_check_page_num" json:"toc_check_page_num"`
	MaxPageNumEachNode  int    `yaml:"max_page_num_each_node" json:"max_page_num_each_node"`
	MaxTokenNumEachNode int    `yaml:"max_token_num_each_node" json:"max_token_num_each_node"`
	IfAddNodeID         string `yaml:"if_add_node_id" json:"if_add_node_id"`
	IfAddNodeSummary    string `yaml:"if_add_node_summary" json:"if_add_node_summary"`
	IfAddDocDescription string `yaml:"if_add_doc_description" json:"if_add_doc_description"`
	IfAddNodeText       string `yaml:"if_add_node_text" json:"if_add_node_text"`
}

func DefaultOptions() Options {
	return Options{
		Model:               "gpt-4o-2024-11-20",
		TOCCheckPageNum:     20,
		MaxPageNumEachNode:  10,
		MaxTokenNumEachNode: 20000,
		IfAddNodeID:         "yes",
		IfAddNodeSummary:    "yes",
		IfAddDocDescription: "no",
		IfAddNodeText:       "no",
	}
}

func Load(path string) (Options, error) {
	opt := DefaultOptions()
	if path == "" {
		return opt, nil
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return opt, fmt.Errorf("read config file: %w", err)
	}
	if err := yaml.Unmarshal(b, &opt); err != nil {
		return opt, fmt.Errorf("parse config yaml: %w", err)
	}
	opt.Normalize()
	return opt, nil
}

func (o *Options) Normalize() {
	if o.Model == "" {
		o.Model = "gpt-4o-2024-11-20"
	}
	o.IfAddNodeID = normalizeYesNo(o.IfAddNodeID, "yes")
	o.IfAddNodeSummary = normalizeYesNo(o.IfAddNodeSummary, "yes")
	o.IfAddDocDescription = normalizeYesNo(o.IfAddDocDescription, "no")
	o.IfAddNodeText = normalizeYesNo(o.IfAddNodeText, "no")
}

func normalizeYesNo(value string, fallback string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	switch v {
	case "yes", "no":
		return v
	case "":
		return fallback
	default:
		return fallback
	}
}

func IsYes(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "yes")
}
