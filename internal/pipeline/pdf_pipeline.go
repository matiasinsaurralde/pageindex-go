package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/VectifyAI/PageIndex/internal/config"
	"github.com/VectifyAI/PageIndex/internal/llm"
	"github.com/VectifyAI/PageIndex/internal/model"
	"github.com/VectifyAI/PageIndex/internal/pdf"
	"github.com/VectifyAI/PageIndex/internal/tokens"
	"github.com/VectifyAI/PageIndex/internal/tree"
	"github.com/VectifyAI/PageIndex/internal/util"
)

var physicalIndexRE = regexp.MustCompile(`<?physical_index_(\d+)>?`)

func RunPDFBytes(ctx context.Context, filename string, data []byte, opt config.Options) (model.Result, error) {
	pages, err := pdf.ExtractPagesWithTokensFromBytes(data, opt.Model)
	if err != nil {
		return model.Result{}, err
	}
	return runFromPages(ctx, filename, pages, opt)
}

func RunPDFReader(ctx context.Context, filename string, r io.Reader, opt config.Options) (model.Result, error) {
	pages, err := pdf.ExtractPagesWithTokensFromReader(r, opt.Model)
	if err != nil {
		return model.Result{}, err
	}
	return runFromPages(ctx, filename, pages, opt)
}

func runFromPages(ctx context.Context, filename string, pages []model.Page, opt config.Options) (model.Result, error) {
	client, err := llm.NewFromEnv()
	if err != nil {
		return model.Result{}, err
	}
	if len(pages) == 0 {
		return model.Result{}, fmt.Errorf("empty pdf content")
	}

	items, err := processNoTOC(ctx, client, pages, opt, 1)
	if err != nil {
		items = fallbackSingleNode(pages)
	}
	items = normalizeTOCItems(items, len(pages), 1)
	if len(items) == 0 {
		items = fallbackSingleNode(pages)
	}

	structure := buildTreeWithBoundaries(items, len(pages))
	if len(structure) == 0 {
		structure = []*model.Node{{
			Title: "Document",
		}}
	}
	structure = wrapWithDocumentRootIfNeeded(structure, pages)

	_ = processLargeNodesRecursively(ctx, client, structure, pages, opt, 0)

	if config.IsYes(opt.IfAddNodeID) {
		tree.AddNodeIDs(structure)
	}

	if config.IsYes(opt.IfAddNodeText) || config.IsYes(opt.IfAddNodeSummary) {
		addNodeText(structure, pages)
	}

	if config.IsYes(opt.IfAddNodeSummary) {
		if err := addNodeSummaries(ctx, client, structure, opt.Model); err != nil {
			return model.Result{}, err
		}
		if !config.IsYes(opt.IfAddNodeText) {
			removeNodeText(structure)
		}
	}

	res := model.Result{
		DocName:   filepath.Base(filename),
		Structure: structure,
	}

	if config.IsYes(opt.IfAddDocDescription) {
		desc, err := generateDocDescription(ctx, client, structure, opt.Model)
		if err != nil {
			return model.Result{}, err
		}
		res.DocDescription = desc
	}

	return res, nil
}

func processNoTOC(ctx context.Context, client *llm.Client, pages []model.Page, opt config.Options, startIndex int) ([]model.TOCItem, error) {
	chunks := buildTaggedChunks(pages, opt.Model, opt.MaxTokenNumEachNode, startIndex)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no chunks generated")
	}

	initialPrompt := `
You are an expert in extracting hierarchical tree structure, your task is to generate the tree structure of the document.

The structure variable is the numeric system which represents the index of the hierarchy section in the table of contents. For example, the first section has structure index 1, the first subsection has structure index 1.1, the second subsection has structure index 1.2, etc.

For the title, you need to extract the original title from the text, only fix the space inconsistency.

The provided text contains tags like <physical_index_X> and <physical_index_X> to indicate the start and end of page X.

For the physical_index, you need to extract the physical index of the start of the section from the text. Keep the <physical_index_X> format.

The response should be in the following format:
[
  {
    "structure": <structure index, "x.x.x"> (string),
    "title": <title of the section, keep the original title>,
    "physical_index": "<physical_index_X>"
  }
]
Directly return the final JSON structure. Do not output anything else.
`
	firstOut, err := client.Chat(ctx, opt.Model, initialPrompt+"\nGiven text:\n"+chunks[0], nil)
	if err != nil {
		return nil, err
	}

	toc, err := parseTOCItems(firstOut)
	if err != nil {
		return nil, err
	}

	for _, chunk := range chunks[1:] {
		continuePrompt := `
You are an expert in extracting hierarchical tree structure.
You are given a tree structure of the previous part and the text of the current part.
Your task is to continue the tree structure from the previous part to include the current part.

The structure variable is the numeric system which represents the index of the hierarchy section in the table of contents.
For the title, keep the original title with only spacing fixes.
The provided text contains tags like <physical_index_X>.
For physical_index, extract the section start physical index and keep <physical_index_X> format.

Return ONLY the additional JSON list:
[
  {
    "structure": "x.x.x",
    "title": "...",
    "physical_index": "<physical_index_X>"
  }
]
`
		historyPrompt := fmt.Sprintf("%s\nGiven text:\n%s\nPrevious tree structure:\n%s", continuePrompt, chunk, marshalPretty(toc))
		addOut, callErr := client.Chat(ctx, opt.Model, historyPrompt, nil)
		if callErr != nil {
			continue
		}
		add, parseErr := parseTOCItems(addOut)
		if parseErr == nil && len(add) > 0 {
			toc = append(toc, add...)
			continue
		}

		repairPrompt := "Please return ONLY a valid JSON array of items with keys structure,title,physical_index."
		repaired, repairErr := client.Chat(ctx, opt.Model, historyPrompt+"\n\n"+repairPrompt, nil)
		if repairErr != nil {
			continue
		}
		add, parseErr = parseTOCItems(repaired)
		if parseErr == nil && len(add) > 0 {
			toc = append(toc, add...)
		}
	}

	toc = normalizeTOCItems(toc, len(pages), startIndex)
	if len(toc) == 0 {
		return nil, fmt.Errorf("empty toc generated")
	}

	_, incorrect := verifyTOC(ctx, client, pages, toc, opt.Model, startIndex)
	if len(incorrect) > 0 {
		toc = fixIncorrectTOCWithRetries(ctx, client, pages, toc, incorrect, opt.Model, startIndex, 2)
	}
	addAppearStart(ctx, client, pages, toc, opt.Model, startIndex)

	return normalizeTOCItems(toc, len(pages), startIndex), nil
}

func buildTaggedChunks(pages []model.Page, modelName string, maxTokens int, startIndex int) []string {
	if maxTokens <= 0 {
		maxTokens = 20000
	}
	parts := make([]string, 0, len(pages))
	lengths := make([]int, 0, len(pages))
	total := 0
	for i, p := range pages {
		pageNo := i + startIndex
		tagged := fmt.Sprintf("<physical_index_%d>\n%s\n<physical_index_%d>\n\n", pageNo, p.Text, pageNo)
		parts = append(parts, tagged)
		t := p.TokenCount
		if t == 0 {
			t = tokens.Count(modelName, tagged)
		}
		lengths = append(lengths, t)
		total += t
	}
	if total <= maxTokens {
		return []string{strings.Join(parts, "")}
	}

	var chunks []string
	var current []string
	currentTokens := 0
	for i := range parts {
		if currentTokens+lengths[i] > maxTokens && len(current) > 0 {
			chunks = append(chunks, strings.Join(current, ""))
			current = current[:0]
			currentTokens = 0
		}
		current = append(current, parts[i])
		currentTokens += lengths[i]
	}
	if len(current) > 0 {
		chunks = append(chunks, strings.Join(current, ""))
	}
	return chunks
}

func normalizeTOCItems(items []model.TOCItem, pageCount int, startIndex int) []model.TOCItem {
	out := make([]model.TOCItem, 0, len(items))
	nextStructure := 1
	for i := range items {
		item := items[i]
		item.Structure = strings.TrimSpace(item.Structure)
		if item.Structure == "" {
			item.Structure = strconv.Itoa(nextStructure)
			nextStructure++
		}
		item.Title = strings.TrimSpace(item.Title)
		if item.Title == "" {
			continue
		}
		if item.PhysicalIndex == nil {
			if idx := parsePhysicalIndexFromTitleFallback(item); idx != nil {
				item.PhysicalIndex = idx
			}
		}
		if item.PhysicalIndex == nil {
			continue
		}
		if *item.PhysicalIndex < startIndex || *item.PhysicalIndex > (pageCount+startIndex-1) {
			continue
		}
		out = append(out, item)
	}

	seen := make(map[string]bool, len(out))
	dedup := make([]model.TOCItem, 0, len(out))
	for _, item := range out {
		key := fmt.Sprintf("%s|%s|%d", item.Structure, item.Title, *item.PhysicalIndex)
		if seen[key] {
			continue
		}
		seen[key] = true
		dedup = append(dedup, item)
	}

	sort.SliceStable(dedup, func(i, j int) bool {
		return *dedup[i].PhysicalIndex < *dedup[j].PhysicalIndex
	})
	return dedup
}

func parseTOCItems(raw string) ([]model.TOCItem, error) {
	clean := util.ExtractJSONString(raw)
	var arr []map[string]any
	if err := json.Unmarshal([]byte(clean), &arr); err != nil {
		var wrapped struct {
			TableOfContents []map[string]any `json:"table_of_contents"`
		}
		if err2 := json.Unmarshal([]byte(clean), &wrapped); err2 != nil {
			return nil, err
		}
		arr = wrapped.TableOfContents
	}

	out := make([]model.TOCItem, 0, len(arr))
	for _, row := range arr {
		item := model.TOCItem{
			Structure: stringAny(row["structure"]),
			Title:     strings.TrimSpace(stringAny(row["title"])),
		}
		if item.Title == "" {
			continue
		}
		if v, ok := intAny(row["physical_index"]); ok {
			val := v
			item.PhysicalIndex = &val
		}
		if v, ok := intAny(row["page"]); ok {
			val := v
			item.Page = &val
		}
		out = append(out, item)
	}
	return out, nil
}

func parsePhysicalIndexFromTitleFallback(item model.TOCItem) *int {
	for _, candidate := range []string{item.Title, item.Structure} {
		m := physicalIndexRE.FindStringSubmatch(candidate)
		if len(m) == 2 {
			v, err := strconv.Atoi(m[1])
			if err == nil {
				return &v
			}
		}
	}
	return nil
}

func fallbackSingleNode(_ []model.Page) []model.TOCItem {
	one := 1
	return []model.TOCItem{
		{
			Structure:     "1",
			Title:         "Document",
			PhysicalIndex: &one,
		},
	}
}

func buildTreeWithBoundaries(items []model.TOCItem, totalPages int) []*model.Node {
	clean := make([]model.TOCItem, 0, len(items))
	for _, item := range items {
		if item.PhysicalIndex != nil && strings.TrimSpace(item.Title) != "" {
			clean = append(clean, item)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	sort.SliceStable(clean, func(i, j int) bool {
		return *clean[i].PhysicalIndex < *clean[j].PhysicalIndex
	})

	nodesByStructure := map[string]*model.Node{}
	order := make([]string, 0, len(clean))
	for i, item := range clean {
		start := *item.PhysicalIndex
		end := totalPages
		if i+1 < len(clean) {
			next := *clean[i+1].PhysicalIndex
			if strings.EqualFold(clean[i+1].AppearStart, "yes") && (next-start) > 1 {
				end = next - 1
			} else {
				end = next
			}
		}
		if end < start {
			end = start
		}
		s, e := start, end
		key := item.Structure
		nodesByStructure[key] = &model.Node{
			Title:      item.Title,
			StartIndex: &s,
			EndIndex:   &e,
		}
		order = append(order, key)
	}

	roots := make([]*model.Node, 0, len(nodesByStructure))
	for _, key := range order {
		node := nodesByStructure[key]
		parentKey := parentStructure(key)
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

func wrapWithDocumentRootIfNeeded(roots []*model.Node, pages []model.Page) []*model.Node {
	if len(roots) == 0 || len(pages) == 0 {
		return roots
	}
	first := roots[0]
	if first.StartIndex == nil || *first.StartIndex <= 1 {
		return roots
	}

	start := 1
	end := *first.StartIndex
	title := inferDocumentTitle(pages[0].Text)
	if title == "" {
		title = "Document"
	}

	root := &model.Node{
		Title:      title,
		StartIndex: &start,
		EndIndex:   &end,
		Nodes:      roots,
	}
	return []*model.Node{root}
}

func inferDocumentTitle(text string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 8 {
			if len(line) > 140 {
				return strings.TrimSpace(line[:140])
			}
			return line
		}
	}
	return ""
}

func parentStructure(s string) string {
	parts := strings.Split(s, ".")
	if len(parts) <= 1 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], ".")
}

func verifyTOC(ctx context.Context, client *llm.Client, pages []model.Page, items []model.TOCItem, modelName string, startIndex int) (float64, []int) {
	if len(items) == 0 {
		return 0, nil
	}
	type result struct {
		idx int
		ok  bool
	}
	results := make([]result, 0, len(items))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)

	for idx := range items {
		if items[idx].PhysicalIndex == nil {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			pageNo := *items[i].PhysicalIndex
			localIdx := pageNo - startIndex
			if localIdx < 0 || localIdx >= len(pages) {
				mu.Lock()
				results = append(results, result{idx: i, ok: false})
				mu.Unlock()
				return
			}
			ok, _ := checkTitleAppearance(ctx, client, items[i].Title, pages[localIdx].Text, modelName)
			mu.Lock()
			results = append(results, result{idx: i, ok: ok})
			mu.Unlock()
		}(idx)
	}
	wg.Wait()
	if len(results) == 0 {
		return 0, nil
	}
	correct := 0
	incorrect := make([]int, 0)
	for _, r := range results {
		if r.ok {
			correct++
		} else {
			incorrect = append(incorrect, r.idx)
		}
	}
	return float64(correct) / float64(len(results)), incorrect
}

func fixIncorrectTOCWithRetries(ctx context.Context, client *llm.Client, pages []model.Page, items []model.TOCItem, incorrect []int, modelName string, startIndex int, maxAttempts int) []model.TOCItem {
	current := items
	currentIncorrect := incorrect
	for attempt := 0; attempt < maxAttempts && len(currentIncorrect) > 0; attempt++ {
		for _, badIdx := range currentIncorrect {
			if badIdx < 0 || badIdx >= len(current) {
				continue
			}
			prev := startIndex
			next := len(pages) + startIndex - 1
			for i := badIdx - 1; i >= 0; i-- {
				if current[i].PhysicalIndex != nil {
					prev = *current[i].PhysicalIndex
					break
				}
			}
			for i := badIdx + 1; i < len(current); i++ {
				if current[i].PhysicalIndex != nil {
					next = *current[i].PhysicalIndex
					break
				}
			}
			content := buildTaggedRangeText(pages, prev, next, startIndex)
			fixedIdx, err := singleTOCItemIndexFixer(ctx, client, current[badIdx].Title, content, modelName)
			if err != nil {
				continue
			}
			if fixedIdx >= startIndex && fixedIdx <= len(pages)+startIndex-1 {
				v := fixedIdx
				current[badIdx].PhysicalIndex = &v
			}
		}
		_, currentIncorrect = verifyTOC(ctx, client, pages, current, modelName, startIndex)
	}
	return current
}

func addAppearStart(ctx context.Context, client *llm.Client, pages []model.Page, items []model.TOCItem, modelName string, startIndex int) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range items {
		if items[i].PhysicalIndex == nil {
			items[i].AppearStart = "no"
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pageNo := *items[idx].PhysicalIndex
			local := pageNo - startIndex
			if local < 0 || local >= len(pages) {
				items[idx].AppearStart = "no"
				return
			}
			ok, _ := checkTitleAppearStart(ctx, client, items[idx].Title, pages[local].Text, modelName)
			if ok {
				items[idx].AppearStart = "yes"
			} else {
				items[idx].AppearStart = "no"
			}
		}(i)
	}
	wg.Wait()
}

func checkTitleAppearance(ctx context.Context, client *llm.Client, title string, pageText string, modelName string) (bool, error) {
	prompt := fmt.Sprintf(`Your job is to check if the given section appears or starts in the given page_text.
Note: do fuzzy matching, ignore any space inconsistency in the page_text.

The given section title is %s.
The given page_text is %s.

Reply format:
{
  "thinking": "...",
  "answer": "yes or no"
}
Directly return the final JSON structure. Do not output anything else.`, title, pageText)
	resp, err := client.Chat(ctx, modelName, prompt, nil)
	if err != nil {
		return false, err
	}
	var out map[string]any
	if err := util.UnmarshalLoose(resp, &out); err != nil {
		return false, err
	}
	return strings.EqualFold(stringAny(out["answer"]), "yes"), nil
}

func checkTitleAppearStart(ctx context.Context, client *llm.Client, title string, pageText string, modelName string) (bool, error) {
	prompt := fmt.Sprintf(`You will be given the current section title and current page_text.
Check if the current section starts in the beginning of page_text.
If there are other contents before the current section title, answer no.

Title: %s
Page text: %s

Reply JSON:
{"start_begin":"yes or no"}`, title, pageText)
	resp, err := client.Chat(ctx, modelName, prompt, nil)
	if err != nil {
		return false, err
	}
	var out map[string]any
	if err := util.UnmarshalLoose(resp, &out); err != nil {
		return false, err
	}
	return strings.EqualFold(stringAny(out["start_begin"]), "yes"), nil
}

func singleTOCItemIndexFixer(ctx context.Context, client *llm.Client, title string, content string, modelName string) (int, error) {
	prompt := fmt.Sprintf(`You are given a section title and several pages of a document, your job is to find the physical index of the start page of the section.
The provided pages contain tags like <physical_index_X>.

Reply JSON:
{
  "thinking":"...",
  "physical_index":"<physical_index_X>"
}

Section title: %s
Document pages:
%s`, title, content)
	resp, err := client.Chat(ctx, modelName, prompt, nil)
	if err != nil {
		return 0, err
	}
	var out map[string]any
	if err := util.UnmarshalLoose(resp, &out); err != nil {
		return 0, err
	}
	v, ok := intAny(out["physical_index"])
	if !ok {
		return 0, fmt.Errorf("missing physical_index")
	}
	return v, nil
}

func buildTaggedRangeText(pages []model.Page, startPage int, endPage int, startIndex int) string {
	if startPage > endPage {
		startPage, endPage = endPage, startPage
	}
	if startPage < startIndex {
		startPage = startIndex
	}
	maxPage := len(pages) + startIndex - 1
	if endPage > maxPage {
		endPage = maxPage
	}
	var b strings.Builder
	for pageNo := startPage; pageNo <= endPage; pageNo++ {
		local := pageNo - startIndex
		if local < 0 || local >= len(pages) {
			continue
		}
		b.WriteString(fmt.Sprintf("<physical_index_%d>\n%s\n<physical_index_%d>\n\n", pageNo, pages[local].Text, pageNo))
	}
	return b.String()
}

func processLargeNodesRecursively(ctx context.Context, client *llm.Client, nodes []*model.Node, pages []model.Page, opt config.Options, depth int) error {
	if depth >= 4 {
		return nil
	}
	for _, node := range nodes {
		if node.StartIndex == nil || node.EndIndex == nil {
			continue
		}
		start := *node.StartIndex
		end := *node.EndIndex
		if start < 1 || end > len(pages) || start > end {
			continue
		}

		tokenNum := 0
		for i := start - 1; i < end; i++ {
			tokenNum += pages[i].TokenCount
		}
		pageSpan := end - start + 1
		if pageSpan > opt.MaxPageNumEachNode && tokenNum >= opt.MaxTokenNumEachNode {
			subPages := pages[start-1 : end]
			subItems, err := processNoTOC(ctx, client, subPages, opt, start)
			if err == nil && len(subItems) > 0 {
				subItems = normalizeTOCItems(subItems, len(subPages), start)
				if len(subItems) > 0 && strings.EqualFold(strings.TrimSpace(subItems[0].Title), strings.TrimSpace(node.Title)) {
					if len(subItems) > 1 {
						subItems = subItems[1:]
					}
				}
				children := buildTreeWithBoundaries(subItems, end)
				if len(children) > 0 {
					node.Nodes = children
				}
			}
		}
		if len(node.Nodes) > 0 {
			_ = processLargeNodesRecursively(ctx, client, node.Nodes, pages, opt, depth+1)
		}
	}
	return nil
}

func stringAny(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.Itoa(int(t))
	case int:
		return strconv.Itoa(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func intAny(v any) (int, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case int:
		return t, true
	case float64:
		return int(t), true
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, false
		}
		if m := physicalIndexRE.FindStringSubmatch(s); len(m) == 2 {
			n, err := strconv.Atoi(m[1])
			return n, err == nil
		}
		n, err := strconv.Atoi(s)
		return n, err == nil
	default:
		n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(t)))
		return n, err == nil
	}
}

func marshalPretty(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func addNodeText(nodes []*model.Node, pages []model.Page) {
	for _, n := range nodes {
		if n.StartIndex != nil && n.EndIndex != nil {
			start := *n.StartIndex
			end := *n.EndIndex
			if start < 1 {
				start = 1
			}
			if end > len(pages) {
				end = len(pages)
			}
			if start <= end {
				var b strings.Builder
				for i := start - 1; i < end; i++ {
					b.WriteString(pages[i].Text)
					b.WriteString("\n")
				}
				n.Text = strings.TrimSpace(b.String())
			}
		}
		if len(n.Nodes) > 0 {
			addNodeText(n.Nodes, pages)
		}
	}
}

func removeNodeText(nodes []*model.Node) {
	for _, n := range nodes {
		n.Text = ""
		if len(n.Nodes) > 0 {
			removeNodeText(n.Nodes)
		}
	}
}

func addNodeSummaries(ctx context.Context, client *llm.Client, structure []*model.Node, modelName string) error {
	nodes := tree.Flatten(structure)
	const maxConcurrentSummaries = 6

	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentSummaries)
	var firstErr error
	var errMu sync.Mutex

	for _, node := range nodes {
		if strings.TrimSpace(node.Text) == "" {
			continue
		}

		errMu.Lock()
		hasErr := firstErr != nil
		errMu.Unlock()
		if hasErr {
			break
		}

		wg.Add(1)
		go func(n *model.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if ctx.Err() != nil {
				return
			}

			prompt := fmt.Sprintf("You are given a part of a document. Generate a concise description of the main points.\n\nPartial Document Text:\n%s\n\nDirectly return the description only.", n.Text)
			summary, err := client.Chat(ctx, modelName, prompt, nil)
			if err != nil {
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
				return
			}
			n.Summary = strings.TrimSpace(summary)
		}(node)
	}

	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return nil
}

func generateDocDescription(ctx context.Context, client *llm.Client, structure []*model.Node, modelName string) (string, error) {
	prompt := fmt.Sprintf("You are an expert in generating descriptions for a document.\nGiven this document structure, generate one sentence that distinguishes this document.\n\nStructure:\n%v\n\nReturn only the sentence.", structure)
	resp, err := client.Chat(ctx, modelName, prompt, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp), nil
}
