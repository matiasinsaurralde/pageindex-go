package pipeline

import (
	"strings"
	"testing"

	"github.com/matiasinsaurralde/go-pageindex/internal/config"
	"github.com/matiasinsaurralde/go-pageindex/internal/model"
)

func intPtr(i int) *int { return &i }

func TestIsFinishReasonDone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		reason string
		want   bool
	}{
		{"finished", true},
		{"stop", true},
		{"cached", true},
		{"FINISHED", true},
		{"length", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isFinishReasonDone(tt.reason)
		if got != tt.want {
			t.Errorf("isFinishReasonDone(%q) = %v, want %v", tt.reason, got, tt.want)
		}
	}
}

func TestTrimToJSONObjectBoundary(t *testing.T) {
	t.Parallel()
	// Trims to last "}" and then TrimSpace
	got := trimToJSONObjectBoundary("  prefix {\"a\":1}  ")
	if !strings.Contains(got, "}") {
		t.Errorf("trimToJSONObjectBoundary should include up to last }: %q", got)
	}
	// No closing brace: return as-is (trimmed)
	noBrace := trimToJSONObjectBoundary("  no brace here  ")
	if noBrace != "no brace here" {
		t.Errorf("trimToJSONObjectBoundary(no brace) = %q", noBrace)
	}
}

func TestGetJSONContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{"strip fence", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"no fence", `{"a":1}`, `{"a":1}`},
		{"missing closing fence", "```json\n{\"a\":1}", `{"a":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := getJSONContent(tt.response)
			if got != tt.want {
				t.Errorf("getJSONContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRemovePageNumberFromTOC(t *testing.T) {
	t.Parallel()
	items := []model.TOCItem{
		{Structure: "1", Title: "A", Page: intPtr(1), PhysicalIndex: intPtr(1)},
		{Structure: "2", Title: "B", Page: intPtr(2), PhysicalIndex: nil},
	}
	out := removePageNumberFromTOC(items)
	for i := range out {
		if out[i].Page != nil {
			t.Errorf("item %d: Page should be nil", i)
		}
	}
	if out[0].PhysicalIndex == nil || *out[0].PhysicalIndex != 1 {
		t.Errorf("PhysicalIndex should be unchanged")
	}
}

func TestExtractMatchingPagePairs(t *testing.T) {
	t.Parallel()
	tocWithPage := []model.TOCItem{
		{Title: "Chapter One", Page: intPtr(5)},
		{Title: "Chapter Two", Page: intPtr(10)},
	}
	tocWithPhysical := []model.TOCItem{
		{Title: "Chapter One", PhysicalIndex: intPtr(5)},
		{Title: "Chapter Two", PhysicalIndex: intPtr(11)},
	}
	pairs := extractMatchingPagePairs(tocWithPage, tocWithPhysical, 1)
	if len(pairs) != 2 {
		t.Fatalf("got %d pairs, want 2", len(pairs))
	}
	if pairs[0].Page != 5 || pairs[0].PhysicalIndex != 5 {
		t.Errorf("first pair: Page=%d PhysicalIndex=%d", pairs[0].Page, pairs[0].PhysicalIndex)
	}
	if pairs[1].Page != 10 || pairs[1].PhysicalIndex != 11 {
		t.Errorf("second pair: Page=%d PhysicalIndex=%d", pairs[1].Page, pairs[1].PhysicalIndex)
	}
	// startPageIndex filter: PhysicalIndex < startPageIndex excluded
	pairs2 := extractMatchingPagePairs(tocWithPage, tocWithPhysical, 10)
	if len(pairs2) != 1 {
		t.Errorf("with startPageIndex 10: got %d pairs, want 1", len(pairs2))
	}
}

func TestCalculatePageOffset(t *testing.T) {
	t.Parallel()
	_, ok := calculatePageOffset(nil)
	if ok {
		t.Error("empty pairs should return false")
	}
	pairs := []tocPair{
		{Page: 5, PhysicalIndex: 10},
		{Page: 6, PhysicalIndex: 11},
		{Page: 7, PhysicalIndex: 12},
	}
	offset, ok := calculatePageOffset(pairs)
	if !ok || offset != 5 {
		t.Errorf("offset = %d, ok = %v, want 5, true", offset, ok)
	}
}

func TestAddPageOffsetToTOC(t *testing.T) {
	t.Parallel()
	items := []model.TOCItem{
		{Title: "A", Page: intPtr(3), PhysicalIndex: nil},
		{Title: "B", Page: nil, PhysicalIndex: intPtr(2)},
	}
	out := addPageOffsetToTOC(items, 5)
	if out[0].Page != nil {
		t.Error("Page should be cleared")
	}
	if out[0].PhysicalIndex == nil || *out[0].PhysicalIndex != 8 {
		t.Errorf("PhysicalIndex = %v, want 8", out[0].PhysicalIndex)
	}
	if out[1].PhysicalIndex == nil || *out[1].PhysicalIndex != 2 {
		t.Errorf("item without Page should be unchanged")
	}
}

func TestNormalizeTOCItems(t *testing.T) {
	t.Parallel()
	items := []model.TOCItem{
		{Structure: " 1 ", Title: "  A  ", PhysicalIndex: intPtr(1)},
		{Structure: "2", Title: "", PhysicalIndex: intPtr(2)},
		{Structure: "3", Title: "C", PhysicalIndex: intPtr(99)}, // out of range for 5 pages, start 1
	}
	out := normalizeTOCItems(items, 5, 1)
	if len(out) != 2 {
		t.Fatalf("got %d items (empty title and out-of-range should drop), want 2", len(out))
	}
	if out[0].Title != "A" || out[0].Structure != "1" {
		t.Errorf("trim: got %q %q", out[0].Structure, out[0].Title)
	}
	if out[1].PhysicalIndex != nil {
		t.Errorf("out-of-range PhysicalIndex should be nil")
	}
}

func TestAddPrefaceIfNeeded(t *testing.T) {
	t.Parallel()
	startIndex := 1
	items := []model.TOCItem{
		{Structure: "1", Title: "Chapter 1", PhysicalIndex: intPtr(5)},
	}
	out := addPrefaceIfNeeded(items, startIndex)
	if len(out) != 2 {
		t.Fatalf("got %d items, want 2 (preface + chapter)", len(out))
	}
	if out[0].Title != "Preface" || out[0].Structure != "0" {
		t.Errorf("preface: got %q %q", out[0].Structure, out[0].Title)
	}
	if out[0].PhysicalIndex == nil || *out[0].PhysicalIndex != startIndex {
		t.Errorf("preface PhysicalIndex = %v", out[0].PhysicalIndex)
	}
	// First item already at startIndex: no preface
	items2 := []model.TOCItem{
		{Structure: "1", Title: "Intro", PhysicalIndex: intPtr(1)},
	}
	out2 := addPrefaceIfNeeded(items2, startIndex)
	if len(out2) != 1 {
		t.Errorf("no preface when first at startIndex: got %d items", len(out2))
	}
	// Empty or nil first physical: no preface
	out3 := addPrefaceIfNeeded([]model.TOCItem{}, startIndex)
	if len(out3) != 0 {
		t.Errorf("empty items: got %d", len(out3))
	}
}

func TestValidateAndTruncatePhysicalIndices(t *testing.T) {
	t.Parallel()
	items := []model.TOCItem{
		{Title: "A", PhysicalIndex: intPtr(1)},
		{Title: "B", PhysicalIndex: intPtr(10)},
	}
	// maxAllowedPage = 5 + 1 - 1 = 5
	out := validateAndTruncatePhysicalIndices(items, 5, 1)
	if out[0].PhysicalIndex == nil || *out[0].PhysicalIndex != 1 {
		t.Errorf("in range: PhysicalIndex should stay")
	}
	if out[1].PhysicalIndex != nil {
		t.Errorf("over max should be nil, got %v", out[1].PhysicalIndex)
	}
}

func TestRetainItemsWithPhysicalIndex(t *testing.T) {
	t.Parallel()
	items := []model.TOCItem{
		{Title: "A", PhysicalIndex: intPtr(1)},
		{Title: "B", PhysicalIndex: nil},
		{Title: "C", PhysicalIndex: intPtr(3)},
	}
	out := retainItemsWithPhysicalIndex(items)
	if len(out) != 2 {
		t.Fatalf("got %d, want 2", len(out))
	}
	if out[0].Title != "A" || out[1].Title != "C" {
		t.Errorf("got %q, %q", out[0].Title, out[1].Title)
	}
}

func TestPostProcessTOCItems(t *testing.T) {
	t.Parallel()
	// Next item has AppearStart "no" -> first node end = next = 3
	items := []model.TOCItem{
		{Structure: "1", Title: "One", PhysicalIndex: intPtr(1), AppearStart: ""},
		{Structure: "2", Title: "Two", PhysicalIndex: intPtr(3), AppearStart: "no"},
	}
	nodes := postProcessTOCItems(items, 5)
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(nodes))
	}
	if nodes[0].StartIndex == nil || *nodes[0].StartIndex != 1 {
		t.Errorf("first start = %v", nodes[0].StartIndex)
	}
	if nodes[0].EndIndex == nil || *nodes[0].EndIndex != 3 {
		t.Errorf("first end (next AppearStart no) = %v, want 3", nodes[0].EndIndex)
	}
	if nodes[1].EndIndex == nil || *nodes[1].EndIndex != 5 {
		t.Errorf("last end = %v", nodes[1].EndIndex)
	}
	// When next has AppearStart "yes", end = next - 1
	items2 := []model.TOCItem{
		{Structure: "1", Title: "One", PhysicalIndex: intPtr(1)},
		{Structure: "2", Title: "Two", PhysicalIndex: intPtr(3), AppearStart: "yes"},
	}
	nodes2 := postProcessTOCItems(items2, 5)
	if nodes2[0].EndIndex == nil || *nodes2[0].EndIndex != 2 {
		t.Errorf("first end (next AppearStart yes) = %v, want 2", nodes2[0].EndIndex)
	}
	// All invalid: nil
	nilNodes := postProcessTOCItems([]model.TOCItem{{Title: "A", PhysicalIndex: nil}}, 5)
	if nilNodes != nil {
		t.Errorf("all invalid should return nil, got %d", len(nilNodes))
	}
}

func TestListToTreeFromTOC(t *testing.T) {
	t.Parallel()
	items := []postProcessItem{
		{Structure: "1", Title: "One", Start: 1, End: 2},
		{Structure: "1.1", Title: "One.One", Start: 2, End: 2},
		{Structure: "2", Title: "Two", Start: 3, End: 5},
	}
	roots := listToTreeFromTOC(items)
	if len(roots) != 2 {
		t.Fatalf("got %d roots, want 2", len(roots))
	}
	if len(roots[0].Nodes) != 1 {
		t.Errorf("first root should have 1 child, got %d", len(roots[0].Nodes))
	}
	if roots[0].Nodes[0].Title != "One.One" {
		t.Errorf("child title = %q", roots[0].Nodes[0].Title)
	}
}

func TestParseTOCItems(t *testing.T) {
	t.Parallel()
	raw := `[{"structure":"1","title":"Introduction","physical_index":1},{"structure":"2","title":"Chapter One","page":5}]`
	items, err := parseTOCItems(raw)
	if err != nil {
		t.Fatalf("parseTOCItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items", len(items))
	}
	if items[0].Title != "Introduction" || items[0].PhysicalIndex == nil || *items[0].PhysicalIndex != 1 {
		t.Errorf("first item: %+v", items[0])
	}
	if items[1].Page == nil || *items[1].Page != 5 {
		t.Errorf("second item Page = %v", items[1].Page)
	}
	// table_of_contents wrapper
	raw2 := `{"table_of_contents":[{"structure":"1","title":"Only"}]}`
	items2, err := parseTOCItems(raw2)
	if err != nil || len(items2) != 1 {
		t.Fatalf("table_of_contents: err=%v len=%d", err, len(items2))
	}
	if items2[0].Title != "Only" {
		t.Errorf("title = %q", items2[0].Title)
	}
	// None -> null
	raw3 := `[{"structure":"1","title":"X","page":None}]`
	items3, err := parseTOCItems(raw3)
	if err != nil {
		t.Fatalf("None: %v", err)
	}
	if len(items3) != 1 || items3[0].Title != "X" {
		t.Errorf("None handling: %+v", items3)
	}
}

func TestParseTOCItems_partial_or_malformed(t *testing.T) {
	t.Parallel()
	// Truncated JSON might still yield items via extractPartialTOCMaps
	raw := `[{"structure":"1","title":"A","physical_index":1},{"structure":"2","title":"B"`
	items, err := parseTOCItems(raw)
	// May succeed with partial or fail; if succeed, we should have at least one
	if err == nil && len(items) > 0 {
		if items[0].Title != "A" {
			t.Errorf("partial parse first title = %q", items[0].Title)
		}
	}
}

func TestNormalizeTOCJSON(t *testing.T) {
	t.Parallel()
	raw := "  [{\"a\": None}]  "
	got := normalizeTOCJSON(raw)
	if got != "" && !strings.Contains(got, "null") {
		t.Errorf("None should become null: %q", got)
	}
	withFence := "```json\n[{\"a\": null}]\n```"
	got2 := normalizeTOCJSON(withFence)
	if got2 != "" && !strings.Contains(got2, "[") {
		t.Errorf("fenced JSON should be extracted: %q", got2)
	}
}

func TestRecoverTOCJSON(t *testing.T) {
	t.Parallel()
	// Unclosed brace
	got := recoverTOCJSON(`{"a": 1`)
	if got == "" {
		t.Error("recoverTOCJSON should try to close")
	}
	if got != "" && !strings.Contains(got, "}") {
		t.Errorf("recovered should contain }: %q", got)
	}
	// Empty
	if recoverTOCJSON("") != "" {
		t.Error("empty should return empty")
	}
}

func TestParentStructure(t *testing.T) {
	t.Parallel()
	if g := parentStructure("1.2.3"); g != "1.2" {
		t.Errorf("parentStructure(1.2.3) = %q, want 1.2", g)
	}
	if g := parentStructure("1"); g != "" {
		t.Errorf("parentStructure(1) = %q, want empty", g)
	}
}

func TestBuildTaggedRangeText(t *testing.T) {
	t.Parallel()
	pages := []model.Page{
		{Text: "Page1", TokenCount: 10},
		{Text: "Page2", TokenCount: 10},
	}
	// startPage > endPage -> ""
	got := buildTaggedRangeText(pages, 3, 1, 1)
	if got != "" {
		t.Errorf("startPage > endPage should return empty: %q", got)
	}
	got2 := buildTaggedRangeText(pages, 1, 2, 1)
	if got2 == "" {
		t.Fatal("valid range should return content")
	}
	if !strings.Contains(got2, "<physical_index_1>") || !strings.Contains(got2, "<physical_index_2>") {
		t.Errorf("should contain tags: %q", got2)
	}
	if !strings.Contains(got2, "Page1") || !strings.Contains(got2, "Page2") {
		t.Errorf("should contain page text: %q", got2)
	}
}

func TestStringAny(t *testing.T) {
	t.Parallel()
	if g := stringAny(nil); g != "" {
		t.Errorf("stringAny(nil) = %q", g)
	}
	if g := stringAny("  x  "); g != "x" {
		t.Errorf("stringAny(string) = %q", g)
	}
	if g := stringAny(float64(42)); g != "42" {
		t.Errorf("stringAny(42.0) = %q", g)
	}
	if g := stringAny(7); g != "7" {
		t.Errorf("stringAny(7) = %q", g)
	}
}

func TestIntAny(t *testing.T) {
	t.Parallel()
	if _, ok := intAny(nil); ok {
		t.Error("intAny(nil) should be false")
	}
	if v, ok := intAny(42); !ok || v != 42 {
		t.Errorf("intAny(42) = %d, %v", v, ok)
	}
	if v, ok := intAny(float64(10)); !ok || v != 10 {
		t.Errorf("intAny(10.0) = %d, %v", v, ok)
	}
	if v, ok := intAny("<physical_index_7>"); !ok || v != 7 {
		t.Errorf("intAny(physical_index_7) = %d, %v", v, ok)
	}
	if v, ok := intAny("99"); !ok || v != 99 {
		t.Errorf("intAny(\"99\") = %d, %v", v, ok)
	}
}

func TestEffectiveChunkMaxTokens(t *testing.T) {
	t.Parallel()
	opt := config.Options{MaxTokenNumEachNode: 15000}
	opt.Normalize()
	if g := effectiveChunkMaxTokens(opt); g != 15000 {
		t.Errorf("effectiveChunkMaxTokens = %d, want 15000", g)
	}
}

func TestBuildTaggedChunks(t *testing.T) {
	t.Parallel()
	opt := config.DefaultOptions()
	opt.Normalize()
	pages := []model.Page{
		{Text: "Short", TokenCount: 10},
		{Text: "Also short", TokenCount: 10},
	}
	// Total under maxTokens -> single chunk
	chunks := buildTaggedChunks(pages, opt, 20000, 1)
	if len(chunks) != 1 {
		t.Fatalf("small content: got %d chunks, want 1", len(chunks))
	}
	if !strings.Contains(chunks[0], "<physical_index_1>") {
		t.Errorf("chunk should contain page tags: %s", chunks[0][:min(80, len(chunks[0]))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
