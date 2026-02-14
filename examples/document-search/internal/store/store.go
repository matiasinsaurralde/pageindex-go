package store

import (
	"sync"
	"time"

	"github.com/matiasinsaurralde/go-pageindex/internal/model"
)

type DocRecord struct {
	DocID      string
	DocName    string
	ChunkCount int
	IndexedAt  time.Time
	CachedTree *model.Result
}

type Catalog struct {
	mu   sync.RWMutex
	docs map[string]DocRecord
}

func NewCatalog() *Catalog {
	return &Catalog{
		docs: make(map[string]DocRecord),
	}
}

func (c *Catalog) Put(record DocRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.docs[record.DocID] = record
}

func (c *Catalog) Delete(docID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, existed := c.docs[docID]
	delete(c.docs, docID)
	return existed
}
