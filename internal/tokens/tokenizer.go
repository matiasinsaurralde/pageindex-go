package tokens

import (
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

var (
	cacheMu sync.RWMutex
	cache   = map[string]*tiktoken.Tiktoken{}
)

func Count(model string, text string) int {
	if text == "" {
		return 0
	}
	enc := getEncoding(model)
	if enc == nil {
		return len(text) / 4
	}
	return len(enc.Encode(text, nil, nil))
}

func getEncoding(model string) *tiktoken.Tiktoken {
	cacheMu.RLock()
	if enc, ok := cache[model]; ok {
		cacheMu.RUnlock()
		return enc
	}
	cacheMu.RUnlock()

	enc, err := tiktoken.EncodingForModel(model)
	if err != nil {
		enc, err = tiktoken.GetEncoding("cl100k_base")
		if err != nil {
			return nil
		}
	}

	cacheMu.Lock()
	cache[model] = enc
	cacheMu.Unlock()
	return enc
}
