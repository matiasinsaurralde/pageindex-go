package httpserver

import (
	"github.com/matiasinsaurralde/go-pageindex/internal/config"
)

// MergeOptions overlays request options onto server defaults. Zero/empty values
// in request are treated as "not set" and keep the server default. Result is normalized.
func MergeOptions(serverDefault config.Options, requestOpts *config.Options) config.Options {
	merged := serverDefault
	if requestOpts == nil {
		merged.Normalize()
		return merged
	}
	if requestOpts.Model != "" {
		merged.Model = requestOpts.Model
	}
	if requestOpts.TOCCheckPageNum > 0 {
		merged.TOCCheckPageNum = requestOpts.TOCCheckPageNum
	}
	if requestOpts.TOCAttempts > 0 {
		merged.TOCAttempts = requestOpts.TOCAttempts
	}
	if requestOpts.MaxPageNumEachNode > 0 {
		merged.MaxPageNumEachNode = requestOpts.MaxPageNumEachNode
	}
	if requestOpts.MaxTokenNumEachNode > 0 {
		merged.MaxTokenNumEachNode = requestOpts.MaxTokenNumEachNode
	}
	if requestOpts.IfAddNodeID != "" {
		merged.IfAddNodeID = requestOpts.IfAddNodeID
	}
	if requestOpts.IfAddNodeSummary != "" {
		merged.IfAddNodeSummary = requestOpts.IfAddNodeSummary
	}
	if requestOpts.IfAddDocDescription != "" {
		merged.IfAddDocDescription = requestOpts.IfAddDocDescription
	}
	if requestOpts.IfAddNodeText != "" {
		merged.IfAddNodeText = requestOpts.IfAddNodeText
	}
	merged.Normalize()
	return merged
}
