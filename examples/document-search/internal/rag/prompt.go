package rag

import (
	"fmt"
	"strings"

	"github.com/matiasinsaurralde/go-pageindex/examples/document-search/internal/api"
)

type ContextChunk struct {
	DocID   string
	DocName string
	NodeID  string
	ChunkID string
	Text    string
}

func FlattenContextChunks(docs []api.RankedDocPayload, maxContext int) []ContextChunk {
	out := make([]ContextChunk, 0, maxContext)
	seen := make(map[string]struct{}, maxContext)
	for _, d := range docs {
		for _, ch := range d.Chunks {
			if len(out) >= maxContext {
				return out
			}
			if _, ok := seen[ch.ChunkID]; ok {
				continue
			}
			seen[ch.ChunkID] = struct{}{}
			out = append(out, ContextChunk{
				DocID:   d.DocID,
				DocName: d.DocName,
				NodeID:  ch.NodeID,
				ChunkID: ch.ChunkID,
				Text:    ch.Text,
			})
		}
	}
	return out
}

func BuildPrompt(question string, chunks []ContextChunk) string {
	var b strings.Builder
	b.WriteString("Question:\n")
	b.WriteString(question)
	b.WriteString("\n\nContext snippets (ranked):\n")
	for i, ch := range chunks {
		b.WriteString(fmt.Sprintf("[%d] [%s:%s] doc=%s node=%s\n", i+1, ch.DocID, ch.ChunkID, ch.DocName, ch.NodeID))
		b.WriteString(ch.Text)
		b.WriteString("\n\n")
	}
	b.WriteString("Answer the question using only the context above. ")
	b.WriteString("If information is missing, say that the context is insufficient.\n")
	return b.String()
}
