package tree

import (
	"testing"

	"github.com/matiasinsaurralde/go-pageindex/internal/model"
)

func intPtr(i int) *int { return &i }

func TestBuildTree_empty_items(t *testing.T) {
	t.Parallel()
	if got := BuildTree(nil, 10); got != nil {
		t.Errorf("BuildTree(nil, 10) = %v, want nil", got)
	}
	if got := BuildTree([]model.TOCItem{}, 10); got != nil {
		t.Errorf("BuildTree([], 10) = %v, want nil", got)
	}
}

func TestBuildTree_skips_empty_title_or_nil_physical_index(t *testing.T) {
	t.Parallel()
	items := []model.TOCItem{
		{Structure: "1", Title: "A", PhysicalIndex: intPtr(1)},
		{Structure: "2", Title: "", PhysicalIndex: intPtr(2)},
		{Structure: "3", Title: "C", PhysicalIndex: nil},
		{Structure: "4", Title: "D", PhysicalIndex: intPtr(4)},
	}
	roots := BuildTree(items, 5)
	if len(roots) != 2 {
		t.Fatalf("BuildTree: got %d roots, want 2 (only A and D)", len(roots))
	}
	if roots[0].Title != "A" || roots[1].Title != "D" {
		t.Errorf("roots = %q, %q; want A, D", roots[0].Title, roots[1].Title)
	}
}

func TestBuildTree_orders_by_physical_index(t *testing.T) {
	t.Parallel()
	items := []model.TOCItem{
		{Structure: "3", Title: "Third", PhysicalIndex: intPtr(3)},
		{Structure: "1", Title: "First", PhysicalIndex: intPtr(1)},
		{Structure: "2", Title: "Second", PhysicalIndex: intPtr(2)},
	}
	roots := BuildTree(items, 5)
	if len(roots) != 3 {
		t.Fatalf("got %d roots, want 3", len(roots))
	}
	if roots[0].Title != "First" || roots[1].Title != "Second" || roots[2].Title != "Third" {
		t.Errorf("order: got %q, %q, %q", roots[0].Title, roots[1].Title, roots[2].Title)
	}
}

func TestBuildTree_sets_start_end(t *testing.T) {
	t.Parallel()
	const totalPages = 10
	items := []model.TOCItem{
		{Structure: "1", Title: "A", PhysicalIndex: intPtr(1)},
		{Structure: "2", Title: "B", PhysicalIndex: intPtr(4)},
	}
	roots := BuildTree(items, totalPages)
	if len(roots) != 2 {
		t.Fatalf("got %d roots", len(roots))
	}
	if roots[0].StartIndex == nil || *roots[0].StartIndex != 1 || roots[0].EndIndex == nil || *roots[0].EndIndex != 3 {
		t.Errorf("first node: start=%v end=%v, want 1 and 3", roots[0].StartIndex, roots[0].EndIndex)
	}
	if roots[1].StartIndex == nil || *roots[1].StartIndex != 4 || roots[1].EndIndex == nil || *roots[1].EndIndex != totalPages {
		t.Errorf("second node: start=%v end=%v, want 4 and %d", roots[1].StartIndex, roots[1].EndIndex, totalPages)
	}
}

func TestBuildTree_hierarchy(t *testing.T) {
	t.Parallel()
	items := []model.TOCItem{
		{Structure: "1", Title: "One", PhysicalIndex: intPtr(1)},
		{Structure: "1.1", Title: "One.One", PhysicalIndex: intPtr(2)},
		{Structure: "1.2", Title: "One.Two", PhysicalIndex: intPtr(3)},
		{Structure: "2", Title: "Two", PhysicalIndex: intPtr(4)},
	}
	roots := BuildTree(items, 5)
	if len(roots) != 2 {
		t.Fatalf("got %d roots, want 2", len(roots))
	}
	if roots[0].Title != "One" || len(roots[0].Nodes) != 2 {
		t.Errorf("first root: title=%q children=%d, want One and 2 children", roots[0].Title, len(roots[0].Nodes))
	}
	if roots[0].Nodes[0].Title != "One.One" || roots[0].Nodes[1].Title != "One.Two" {
		t.Errorf("children = %q, %q", roots[0].Nodes[0].Title, roots[0].Nodes[1].Title)
	}
	if roots[1].Title != "Two" || len(roots[1].Nodes) != 0 {
		t.Errorf("second root: title=%q nodes=%d", roots[1].Title, len(roots[1].Nodes))
	}
}

func TestAddNodeIDs(t *testing.T) {
	t.Parallel()
	roots := []*model.Node{
		{Title: "A", Nodes: []*model.Node{
			{Title: "A1"},
			{Title: "A2"},
		}},
		{Title: "B"},
	}
	AddNodeIDs(roots)
	if roots[0].NodeID != "0000" {
		t.Errorf("roots[0].NodeID = %q, want 0000", roots[0].NodeID)
	}
	if roots[0].Nodes[0].NodeID != "0001" || roots[0].Nodes[1].NodeID != "0002" {
		t.Errorf("children NodeIDs = %q, %q", roots[0].Nodes[0].NodeID, roots[0].Nodes[1].NodeID)
	}
	if roots[1].NodeID != "0003" {
		t.Errorf("roots[1].NodeID = %q, want 0003", roots[1].NodeID)
	}
}

func TestFlatten(t *testing.T) {
	t.Parallel()
	roots := []*model.Node{
		{Title: "A", Nodes: []*model.Node{
			{Title: "A1"},
		}},
		{Title: "B"},
	}
	flat := Flatten(roots)
	if len(flat) != 3 {
		t.Fatalf("Flatten: got %d nodes, want 3", len(flat))
	}
	want := []string{"A", "A1", "B"}
	for i, title := range want {
		if flat[i].Title != title {
			t.Errorf("flat[%d].Title = %q, want %q", i, flat[i].Title, title)
		}
	}
}
