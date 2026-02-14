package search

import (
	"context"
	"math"
	"sort"
	"strings"

	"github.com/matiasinsaurralde/go-pageindex/examples/document-search/internal/api"
	"github.com/philippgille/chromem-go"
)

type Service struct {
	Collection *chromem.Collection
}

func (s *Service) Query(ctx context.Context, query string, topKChunks, topNDocs, maxChunksPerDoc int) ([]api.RankedDocPayload, error) {
	collectionSize := s.Collection.Count()
	if collectionSize == 0 {
		return []api.RankedDocPayload{}, nil
	}
	if topKChunks > collectionSize {
		topKChunks = collectionSize
	}

	hits, err := s.Collection.Query(ctx, query, topKChunks, nil, nil)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return []api.RankedDocPayload{}, nil
	}

	grouped := make(map[string][]api.ChunkMatchPayload)
	docNames := make(map[string]string)
	for _, h := range hits {
		docID := h.Metadata["doc_id"]
		if docID == "" {
			continue
		}
		docNames[docID] = h.Metadata["doc_name"]
		grouped[docID] = append(grouped[docID], api.ChunkMatchPayload{
			DocID:      docID,
			DocName:    h.Metadata["doc_name"],
			ChunkID:    h.ID,
			NodeID:     h.Metadata["node_id"],
			ChunkScore: float64(h.Similarity),
			Text:       strings.TrimSpace(h.Content),
		})
	}

	ranked := make([]api.RankedDocPayload, 0, len(grouped))
	for docID, chunks := range grouped {
		sort.Slice(chunks, func(i, j int) bool {
			return chunks[i].ChunkScore > chunks[j].ChunkScore
		})
		hitCount := len(chunks)
		docScore := computeDocScore(chunks)
		if len(chunks) > maxChunksPerDoc {
			chunks = chunks[:maxChunksPerDoc]
		}
		ranked = append(ranked, api.RankedDocPayload{
			DocID:    docID,
			DocName:  docNames[docID],
			DocScore: docScore,
			HitCount: hitCount,
			Chunks:   chunks,
		})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].DocScore == ranked[j].DocScore {
			return ranked[i].DocID < ranked[j].DocID
		}
		return ranked[i].DocScore > ranked[j].DocScore
	})
	if len(ranked) > topNDocs {
		ranked = ranked[:topNDocs]
	}

	return ranked, nil
}

func computeDocScore(chunks []api.ChunkMatchPayload) float64 {
	if len(chunks) == 0 {
		return 0
	}
	total := 0.0
	for _, c := range chunks {
		total += c.ChunkScore
	}
	return (1.0 / math.Sqrt(float64(len(chunks))+1.0)) * total
}
