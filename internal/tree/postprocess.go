package tree

import (
	"fmt"
	"sort"
	"strings"

	"github.com/VectifyAI/PageIndex/internal/model"
)

func BuildTree(items []model.TOCItem, totalPages int) []*model.Node {
	clean := make([]model.TOCItem, 0, len(items))
	for i := range items {
		if items[i].PhysicalIndex == nil || items[i].Title == "" {
			continue
		}
		clean = append(clean, items[i])
	}
	if len(clean) == 0 {
		return nil
	}

	sort.SliceStable(clean, func(i, j int) bool {
		return *clean[i].PhysicalIndex < *clean[j].PhysicalIndex
	})

	nodesByStructure := make(map[string]*model.Node, len(clean))
	roots := make([]*model.Node, 0, len(clean))

	for i := range clean {
		start := *clean[i].PhysicalIndex
		end := totalPages
		if i+1 < len(clean) && clean[i+1].PhysicalIndex != nil {
			next := *clean[i+1].PhysicalIndex
			if next > start {
				end = next - 1
			} else {
				end = start
			}
		}
		startCopy, endCopy := start, end
		key := clean[i].Structure
		if strings.TrimSpace(key) == "" {
			key = fmt.Sprintf("%d", i+1)
		}
		node := &model.Node{
			Title:      clean[i].Title,
			StartIndex: &startCopy,
			EndIndex:   &endCopy,
		}
		nodesByStructure[key] = node
	}

	keys := make([]string, 0, len(nodesByStructure))
	for k := range nodesByStructure {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		node := nodesByStructure[k]
		parentKey := parentStructure(k)
		if parentKey == "" {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodesByStructure[parentKey]
		if !ok {
			roots = append(roots, node)
			continue
		}
		parent.Nodes = append(parent.Nodes, node)
	}

	return roots
}

func AddNodeIDs(nodes []*model.Node) {
	id := 0
	var walk func(list []*model.Node)
	walk = func(list []*model.Node) {
		for _, n := range list {
			n.NodeID = fmt.Sprintf("%04d", id)
			id++
			if len(n.Nodes) > 0 {
				walk(n.Nodes)
			}
		}
	}
	walk(nodes)
}

func parentStructure(s string) string {
	parts := strings.Split(s, ".")
	if len(parts) <= 1 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], ".")
}

func Flatten(nodes []*model.Node) []*model.Node {
	out := make([]*model.Node, 0, len(nodes))
	var walk func(list []*model.Node)
	walk = func(list []*model.Node) {
		for _, n := range list {
			out = append(out, n)
			if len(n.Nodes) > 0 {
				walk(n.Nodes)
			}
		}
	}
	walk(nodes)
	return out
}
