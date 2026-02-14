package ingest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/matiasinsaurralde/go-pageindex/internal/model"
	"github.com/philippgille/chromem-go"
)

type ChunkRecord struct {
	ChunkID string
	DocID   string
	DocName string
	NodeID  string
	Path    string
	Text    string
}

func NewDocID() string {
	var raw [4]byte
	_, _ = rand.Read(raw[:])
	return fmt.Sprintf("doc_%d_%s", time.Now().UnixNano(), hex.EncodeToString(raw[:]))
}

func ExtractChunks(docID, docName string, nodes []*model.Node) []ChunkRecord {
	out := make([]ChunkRecord, 0, 64)
	var idx int
	var walk func(path []string, node *model.Node)
	walk = func(path []string, node *model.Node) {
		if node == nil {
			return
		}
		localPath := path
		title := strings.TrimSpace(node.Title)
		if title != "" {
			localPath = append(localPath, title)
		}

		text := chunkText(node)
		if text != "" {
			idx++
			chunkID := fmt.Sprintf("%s:%d", docID, idx)
			out = append(out, ChunkRecord{
				ChunkID: chunkID,
				DocID:   docID,
				DocName: docName,
				NodeID:  strings.TrimSpace(node.NodeID),
				Path:    strings.Join(localPath, " > "),
				Text:    text,
			})
		}

		for _, child := range node.Nodes {
			walk(localPath, child)
		}
	}
	for _, node := range nodes {
		walk(nil, node)
	}
	return out
}

func ToChromemDocs(chunks []ChunkRecord) []chromem.Document {
	docs := make([]chromem.Document, 0, len(chunks))
	for _, ch := range chunks {
		docs = append(docs, chromem.Document{
			ID:      ch.ChunkID,
			Content: ch.Text,
			Metadata: map[string]string{
				"doc_id":   ch.DocID,
				"doc_name": ch.DocName,
				"node_id":  ch.NodeID,
				"path":     ch.Path,
			},
		})
	}
	return docs
}

func chunkText(node *model.Node) string {
	if node == nil {
		return ""
	}
	text := strings.TrimSpace(node.Text)
	summary := strings.TrimSpace(node.Summary)
	title := strings.TrimSpace(node.Title)

	switch {
	case text != "" && summary != "":
		return fmt.Sprintf("%s\n\nSummary: %s", text, summary)
	case text != "":
		return text
	case summary != "" && title != "":
		return fmt.Sprintf("%s\n\nSummary: %s", title, summary)
	case summary != "":
		return summary
	default:
		return title
	}
}
