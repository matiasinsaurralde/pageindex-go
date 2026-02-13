package tokens

import (
	"fmt"
	"sync"
	"testing"
)

func TestCount(t *testing.T) {
	t.Run("returns zero for empty text", func(t *testing.T) {
		t.Parallel()

		got := Count("gpt-4o-2024-11-20", "")
		if got != 0 {
			t.Fatalf("Count() = %d, want 0", got)
		}
	})

	t.Run("matches encoder token count for known model", func(t *testing.T) {
		t.Parallel()

		const model = "gpt-4o-2024-11-20"
		text := "Token counting should be deterministic for this sentence."
		enc := getEncoding(model)
		if enc == nil {
			t.Fatal("getEncoding() returned nil for known model")
		}
		want := len(enc.Encode(text, nil, nil))
		got := Count(model, text)
		if got != want {
			t.Fatalf("Count() = %d, want %d", got, want)
		}
	})

	t.Run("falls back to len(text)/4 when cache has nil encoding", func(t *testing.T) {
		model := "test-nil-cache-model"
		text := "1234567890"
		want := len(text) / 4

		cacheMu.Lock()
		prev, existed := cache[model]
		cache[model] = nil
		cacheMu.Unlock()
		t.Cleanup(func() {
			cacheMu.Lock()
			if existed {
				cache[model] = prev
			} else {
				delete(cache, model)
			}
			cacheMu.Unlock()
		})

		got := Count(model, text)
		if got != want {
			t.Fatalf("Count() = %d, want %d", got, want)
		}
	})
}

func TestGetEncodingCachesByModel(t *testing.T) {
	t.Parallel()

	model := fmt.Sprintf("cache-model-%s", t.Name())
	cacheMu.Lock()
	delete(cache, model)
	cacheMu.Unlock()

	first := getEncoding(model)
	if first == nil {
		t.Fatal("first getEncoding() returned nil")
	}
	second := getEncoding(model)
	if second == nil {
		t.Fatal("second getEncoding() returned nil")
	}
	if first != second {
		t.Fatal("expected cached encoder pointer to be reused")
	}
}

func TestCountConcurrentAccess(t *testing.T) {
	t.Parallel()

	const goroutines = 24
	const model = "gpt-4o-2024-11-20"
	text := "Concurrent counting should be thread-safe."

	var wg sync.WaitGroup
	results := make(chan int, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- Count(model, text)
		}()
	}
	wg.Wait()
	close(results)

	expected := -1
	for got := range results {
		if got <= 0 {
			t.Fatalf("Count() = %d, want > 0", got)
		}
		if expected == -1 {
			expected = got
			continue
		}
		if got != expected {
			t.Fatalf("inconsistent token counts: got %d and %d", got, expected)
		}
	}
}
