package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/matiasinsaurralde/go-pageindex/internal/config"
	"github.com/matiasinsaurralde/go-pageindex/internal/llm"
	"github.com/matiasinsaurralde/go-pageindex/internal/model"
	"github.com/matiasinsaurralde/go-pageindex/internal/pdf"
	"github.com/matiasinsaurralde/go-pageindex/internal/tokens"
	"github.com/matiasinsaurralde/go-pageindex/internal/tree"
	"github.com/matiasinsaurralde/go-pageindex/internal/util"
)

var physicalIndexRE = regexp.MustCompile(`<?physical_index_(\d+)>?`)
var tocLeaderDotsRE = regexp.MustCompile(`\.{5,}`)
var tocTrailingCommaRE = regexp.MustCompile(`,\s*([}\]])`)

type tocCheckResult struct {
	TOCContent          string
	TOCPageList         []int
	PageIndexGivenInTOC string
}

func RunPDFBytes(ctx context.Context, filename string, data []byte, opt config.Options) (model.Result, error) {
	pages, err := pdf.ExtractPagesWithTokensFromBytesWithOptions(data, opt)
	if err != nil {
		return model.Result{}, err
	}
	return runFromPages(ctx, filename, pages, opt)
}

func RunPDFReader(ctx context.Context, filename string, r io.Reader, opt config.Options) (model.Result, error) {
	pages, err := pdf.ExtractPagesWithTokensFromReaderWithOptions(r, opt)
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
	defer func() { _ = client.Close() }()
	if len(pages) == 0 {
		return model.Result{}, fmt.Errorf("empty pdf content")
	}

	tocCheck := checkTOC(ctx, client, pages, opt)
	items, metaErr := metaProcessTOC(ctx, client, pages, tocCheck, opt, 1)
	items = normalizeTOCItems(items, len(pages), 1)
	if metaErr != nil {
		return model.Result{}, metaErr
	}
	if len(items) == 0 {
		return model.Result{}, fmt.Errorf("meta processor returned empty toc items")
	}
	items = addPrefaceIfNeeded(items, 1)
	items = validateAndTruncatePhysicalIndices(items, len(pages), 1)
	if len(items) == 0 {
		return model.Result{}, fmt.Errorf("toc items empty after validation")
	}
	addAppearStart(ctx, client, pages, items, opt.Model, 1)

	structure := postProcessTOCItems(items, len(pages))
	if len(structure) == 0 {
		return model.Result{}, fmt.Errorf("post processing produced empty structure")
	}

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

func metaProcessTOC(ctx context.Context, client *llm.Client, pages []model.Page, toc tocCheckResult, opt config.Options, startIndex int) ([]model.TOCItem, error) {
	mode := "process_no_toc"
	if strings.TrimSpace(toc.TOCContent) != "" && strings.EqualFold(strings.TrimSpace(toc.PageIndexGivenInTOC), "yes") {
		mode = "process_toc_with_page_numbers"
	}
	return metaProcessTOCMode(ctx, client, pages, toc, opt, startIndex, mode)
}

func metaProcessTOCMode(ctx context.Context, client *llm.Client, pages []model.Page, toc tocCheckResult, opt config.Options, startIndex int, mode string) ([]model.TOCItem, error) {
	var (
		items []model.TOCItem
		err   error
	)
	switch mode {
	case "process_toc_with_page_numbers":
		items, err = processTOCWithPageNumbers(ctx, client, pages, toc, opt, startIndex)
	case "process_toc_no_page_numbers":
		items, err = processTOCNoPageNumbers(ctx, client, pages, toc, opt, startIndex)
	default:
		items, err = processNoTOC(ctx, client, pages, opt, startIndex)
	}
	if err != nil {
		return nil, err
	}
	items = normalizeTOCItems(items, len(pages), startIndex)
	// Match Python meta_processor: verify/fix only items with physical_index.
	items = retainItemsWithPhysicalIndex(items)
	items = validateAndTruncatePhysicalIndices(items, len(pages), startIndex)
	items = retainItemsWithPhysicalIndex(items)
	if len(items) == 0 {
		switch mode {
		case "process_toc_with_page_numbers":
			return metaProcessTOCMode(ctx, client, pages, toc, opt, startIndex, "process_toc_no_page_numbers")
		case "process_toc_no_page_numbers":
			return metaProcessTOCMode(ctx, client, pages, toc, opt, startIndex, "process_no_toc")
		default:
			return nil, fmt.Errorf("processing failed")
		}
	}
	acc, incorrect := verifyTOC(ctx, client, pages, items, opt.Model, startIndex)
	if acc == 1.0 && len(incorrect) == 0 {
		return items, nil
	}
	if acc > 0.6 && len(incorrect) > 0 {
		fixed := fixIncorrectTOCWithRetries(ctx, client, pages, items, incorrect, opt.Model, startIndex, 3)
		fixed = normalizeTOCItems(fixed, len(pages), startIndex)
		fixed = retainItemsWithPhysicalIndex(fixed)
		fixed = validateAndTruncatePhysicalIndices(fixed, len(pages), startIndex)
		fixed = retainItemsWithPhysicalIndex(fixed)
		return fixed, nil
	}
	switch mode {
	case "process_toc_with_page_numbers":
		return metaProcessTOCMode(ctx, client, pages, toc, opt, startIndex, "process_toc_no_page_numbers")
	case "process_toc_no_page_numbers":
		return metaProcessTOCMode(ctx, client, pages, toc, opt, startIndex, "process_no_toc")
	default:
		return nil, fmt.Errorf("processing failed")
	}
}

func processTOCWithPageNumbers(ctx context.Context, client *llm.Client, pages []model.Page, toc tocCheckResult, opt config.Options, startIndex int) ([]model.TOCItem, error) {
	tocWithPageNumber, err := tocTransformer(ctx, client, toc.TOCContent, opt.Model)
	if err != nil {
		return nil, err
	}
	if len(tocWithPageNumber) == 0 {
		return nil, fmt.Errorf("toc_with_page_numbers transformer returned empty toc")
	}

	tocNoPageNumber := removePageNumberFromTOC(tocWithPageNumber)
	startPageIndex := 0
	if len(toc.TOCPageList) > 0 {
		startPageIndex = toc.TOCPageList[len(toc.TOCPageList)-1] + 1
	}
	limit := startPageIndex + opt.TOCCheckPageNum
	if limit > len(pages) {
		limit = len(pages)
	}

	var mainContent strings.Builder
	for pageIndex := startPageIndex; pageIndex < limit; pageIndex++ {
		pageNo := pageIndex + startIndex
		mainContent.WriteString(fmt.Sprintf("<physical_index_%d>\n%s\n<physical_index_%d>\n\n", pageNo, pages[pageIndex].Text, pageNo))
	}

	tocWithPhysicalIndex, err := tocIndexExtractor(ctx, client, tocNoPageNumber, mainContent.String(), opt.Model)
	if err != nil {
		return nil, err
	}
	pairs := extractMatchingPagePairs(tocWithPageNumber, tocWithPhysicalIndex, startPageIndex+startIndex)
	offset, ok := calculatePageOffset(pairs)
	if ok {
		tocWithPageNumber = addPageOffsetToTOC(tocWithPageNumber, offset)
	}
	tocWithPageNumber = processNonePageNumbers(ctx, client, tocWithPageNumber, pages, startIndex, opt.Model)
	return normalizeTOCItems(tocWithPageNumber, len(pages), startIndex), nil
}

func processTOCNoPageNumbers(ctx context.Context, client *llm.Client, pages []model.Page, toc tocCheckResult, opt config.Options, startIndex int) ([]model.TOCItem, error) {
	base, err := tocTransformer(ctx, client, toc.TOCContent, opt.Model)
	if err != nil {
		return nil, err
	}
	if len(base) == 0 {
		return nil, fmt.Errorf("toc transformer returned empty toc")
	}

	groups := buildTaggedChunks(pages, opt, effectiveChunkMaxTokens(opt), startIndex)
	current := base
	for _, group := range groups {
		updated, callErr := addPageNumberToTOC(ctx, client, group, current, opt.Model)
		if callErr != nil {
			continue
		}
		if len(updated) > 0 {
			current = updated
		}
	}
	current = normalizeTOCItems(current, len(pages), startIndex)
	if len(current) == 0 {
		return nil, fmt.Errorf("toc_no_page_numbers produced empty toc")
	}
	return current, nil
}

func checkTOC(ctx context.Context, client *llm.Client, pages []model.Page, opt config.Options) tocCheckResult {
	tocPages := findTOCPages(ctx, client, pages, 0, opt)
	if len(tocPages) == 0 {
		return tocCheckResult{PageIndexGivenInTOC: "no"}
	}
	tocContent, hasPageIndex := tocExtractor(ctx, client, pages, tocPages, opt.Model)
	if strings.EqualFold(hasPageIndex, "yes") {
		return tocCheckResult{
			TOCContent:          tocContent,
			TOCPageList:         tocPages,
			PageIndexGivenInTOC: "yes",
		}
	}

	currentStart := tocPages[len(tocPages)-1] + 1
	for currentStart < len(pages) && currentStart < opt.TOCCheckPageNum {
		additional := findTOCPages(ctx, client, pages, currentStart, opt)
		if len(additional) == 0 {
			break
		}
		newContent, newHasPage := tocExtractor(ctx, client, pages, additional, opt.Model)
		if strings.EqualFold(newHasPage, "yes") {
			return tocCheckResult{
				TOCContent:          newContent,
				TOCPageList:         additional,
				PageIndexGivenInTOC: "yes",
			}
		}
		currentStart = additional[len(additional)-1] + 1
	}

	return tocCheckResult{
		TOCContent:          tocContent,
		TOCPageList:         tocPages,
		PageIndexGivenInTOC: "no",
	}
}

func findTOCPages(ctx context.Context, client *llm.Client, pages []model.Page, startPageIndex int, opt config.Options) []int {
	lastPageIsTOC := false
	tocPages := make([]int, 0)
	i := startPageIndex
	for i < len(pages) {
		if i >= opt.TOCCheckPageNum && !lastPageIsTOC {
			break
		}
		detected, err := tocDetectorSinglePage(ctx, client, pages[i].Text, opt.Model)
		if err != nil {
			i++
			continue
		}
		if detected {
			tocPages = append(tocPages, i)
			lastPageIsTOC = true
			i++
			continue
		}
		if lastPageIsTOC {
			break
		}
		i++
	}
	return tocPages
}

func tocDetectorSinglePage(ctx context.Context, client *llm.Client, content string, modelName string) (bool, error) {
	prompt := fmt.Sprintf(`Your job is to detect if there is a table of content provided in the given text.

Given text: %s

Return JSON:
{
  "thinking": "...",
  "toc_detected": "yes or no"
}
Directly return the final JSON structure. Do not output anything else.
Note: abstract, summary, notation list, figure list, table list, etc. are not table of contents.`, content)
	resp, err := client.Chat(ctx, modelName, prompt, nil)
	if err != nil {
		return false, err
	}
	var out map[string]any
	if err := util.UnmarshalLoose(resp, &out); err != nil {
		return false, err
	}
	return strings.EqualFold(stringAny(out["toc_detected"]), "yes"), nil
}

func tocExtractor(ctx context.Context, client *llm.Client, pages []model.Page, tocPageList []int, modelName string) (string, string) {
	var b strings.Builder
	for _, pageIndex := range tocPageList {
		if pageIndex < 0 || pageIndex >= len(pages) {
			continue
		}
		b.WriteString(pages[pageIndex].Text)
	}
	tocContent := tocLeaderDotsRE.ReplaceAllString(b.String(), ": ")
	pageIndexGiven, err := detectPageIndexInTOC(ctx, client, tocContent, modelName)
	if err != nil {
		return tocContent, "no"
	}
	return tocContent, pageIndexGiven
}

func detectPageIndexInTOC(ctx context.Context, client *llm.Client, tocContent string, modelName string) (string, error) {
	prompt := fmt.Sprintf(`You will be given a table of contents.

Your job is to detect if there are page numbers/indices given within the table of contents.

Given text: %s

Reply JSON:
{
  "thinking": "...",
  "page_index_given_in_toc": "yes or no"
}
Directly return the final JSON structure. Do not output anything else.`, tocContent)
	resp, err := client.Chat(ctx, modelName, prompt, nil)
	if err != nil {
		return "", err
	}
	var out map[string]any
	if err := util.UnmarshalLoose(resp, &out); err != nil {
		return "", err
	}
	val := strings.ToLower(strings.TrimSpace(stringAny(out["page_index_given_in_toc"])))
	if val != "yes" && val != "no" {
		return "no", nil
	}
	return val, nil
}

func tocTransformer(ctx context.Context, client *llm.Client, tocContent string, modelName string) ([]model.TOCItem, error) {
	initPrompt := fmt.Sprintf(`
    You are given a table of contents, You job is to transform the whole table of content into a JSON format included table_of_contents.

    structure is the numeric system which represents the index of the hierarchy section in the table of contents. For example, the first section has structure index 1, the first subsection has structure index 1.1, the second subsection has structure index 1.2, etc.

    The response should be in the following JSON format:
    {
    table_of_contents: [
        {
            "structure": <structure index, "x.x.x" or None> (string),
            "title": <title of the section>,
            "page": <page number or None>,
        },
        ...
        ],
    }
    You should transform the full table of contents in one go.
    Directly return the final JSON structure, do not output anything else.

 Given table of contents
:%s`, tocContent)
	resp, finishReason, err := client.ChatWithFinishReason(ctx, modelName, initPrompt, nil)
	if err != nil {
		return nil, err
	}

	last := getJSONContent(resp)
	isComplete, _ := checkTOCTransformationComplete(ctx, client, tocContent, last, modelName)
	// Match Python toc_transformer loop semantics:
	// continue until both completeness check and finish-reason indicate done.
	// Keep a guard to avoid unbounded loops on malformed responses.
	for attempt := 0; !isComplete || !isFinishReasonDone(finishReason); attempt++ {
		if attempt >= 8 {
			break
		}
		last = trimToJSONObjectBoundary(last)
		continuePrompt := fmt.Sprintf(`Your task is to continue the table of contents json structure, directly output the remaining part of the json structure.
The response should be in the following JSON format:

The raw table of contents json structure is:
%s

The incomplete transformed table of contents json structure is:
%s

Please continue the json structure, directly output the remaining part of the json structure.`, tocContent, last)
		addition, nextFinishReason, addErr := client.ChatWithFinishReason(ctx, modelName, continuePrompt, nil)
		if addErr != nil {
			break
		}
		chunk := getJSONContent(strings.TrimSpace(addition))
		if chunk == "" {
			chunk = strings.TrimSpace(addition)
		}
		if chunk != "" {
			last = strings.TrimSpace(last + chunk)
		}
		finishReason = nextFinishReason
		isComplete, _ = checkTOCTransformationComplete(ctx, client, tocContent, last, modelName)
	}

	return parseTOCItems(last)
}

func isFinishReasonDone(reason string) bool {
	r := strings.ToLower(strings.TrimSpace(reason))
	return r == "finished" || r == "stop" || r == "cached"
}

func trimToJSONObjectBoundary(s string) string {
	s = strings.TrimSpace(s)
	pos := strings.LastIndex(s, "}")
	if pos == -1 {
		return s
	}
	end := pos + 2
	if end > len(s) {
		end = len(s)
	}
	return strings.TrimSpace(s[:end])
}

// Match Python get_json_content behavior used by toc_transformer continuation:
// strip opening ```json fence even when the closing fence is missing.
func getJSONContent(response string) string {
	startIdx := strings.Index(response, "```json")
	if startIdx != -1 {
		startIdx += len("```json")
		response = response[startIdx:]
	}
	endIdx := strings.LastIndex(response, "```")
	if endIdx != -1 {
		response = response[:endIdx]
	}
	return strings.TrimSpace(response)
}

func checkTOCTransformationComplete(ctx context.Context, client *llm.Client, tocContent string, transformed string, modelName string) (bool, error) {
	prompt := fmt.Sprintf(`
    You are given a raw table of contents and a  table of contents.
    Your job is to check if the  table of contents is complete.

    Reply format:
    {
        "thinking": <why do you think the cleaned table of contents is complete or not>
        "completed": "yes" or "no"
    }
    Directly return the final JSON structure. Do not output anything else.

 Raw Table of contents:
%s
 Cleaned Table of contents:
%s`, tocContent, transformed)
	resp, err := client.Chat(ctx, modelName, prompt, nil)
	if err != nil {
		return false, err
	}
	var out map[string]any
	if err := util.UnmarshalLoose(resp, &out); err != nil {
		return false, err
	}
	decision := strings.TrimSpace(stringAny(out["completed"]))
	if decision == "" {
		decision = strings.TrimSpace(stringAny(out["if_complete"]))
	}
	return strings.EqualFold(decision, "yes"), nil
}

func addPageNumberToTOC(ctx context.Context, client *llm.Client, part string, structure []model.TOCItem, modelName string) ([]model.TOCItem, error) {
	prompt := fmt.Sprintf(`You are given a JSON structure of a document and a partial part of the document.
Check if each section title starts in this partial document.

The provided text contains tags like <physical_index_X>.
If a section starts in this partial content, set its "physical_index" to "<physical_index_X>".
Do not remove existing fields and do not invent sections.
Return the full updated JSON array only.

Current Partial Document:
%s

Given Structure:
%s`, part, marshalPretty(structure))
	resp, err := client.Chat(ctx, modelName, prompt, nil)
	if err != nil {
		return nil, err
	}
	updated, err := parseTOCItems(resp)
	if err != nil {
		return nil, err
	}
	// Merge updates into original list by stable order.
	merged := make([]model.TOCItem, 0, len(structure))
	for i := 0; i < len(structure); i++ {
		if i >= len(updated) {
			merged = append(merged, structure[i])
			continue
		}
		base := structure[i]
		upd := updated[i]
		if strings.TrimSpace(upd.Structure) != "" {
			base.Structure = strings.TrimSpace(upd.Structure)
		}
		if strings.TrimSpace(upd.Title) != "" {
			base.Title = strings.TrimSpace(upd.Title)
		}
		if upd.PhysicalIndex != nil {
			base.PhysicalIndex = upd.PhysicalIndex
		}
		if upd.Page != nil {
			base.Page = upd.Page
		}
		merged = append(merged, base)
	}
	return merged, nil
}

func removePageNumberFromTOC(items []model.TOCItem) []model.TOCItem {
	out := make([]model.TOCItem, 0, len(items))
	for _, it := range items {
		c := it
		c.Page = nil
		out = append(out, c)
	}
	return out
}

func tocIndexExtractor(ctx context.Context, client *llm.Client, toc []model.TOCItem, content string, modelName string) ([]model.TOCItem, error) {
	prompt := fmt.Sprintf(`You are given a table of contents in JSON format and several pages of a document.
Add "physical_index" to sections that appear in the provided pages.

The pages contain tags like <physical_index_X>.
Only add physical_index when the section is in the provided pages.
If not in provided pages, do not add physical_index.
Return only the JSON array.

Table of contents:
%s

Document pages:
%s`, marshalPretty(toc), content)
	resp, err := client.Chat(ctx, modelName, prompt, nil)
	if err != nil {
		return nil, err
	}
	return parseTOCItems(resp)
}

type tocPair struct {
	Page          int
	PhysicalIndex int
}

func extractMatchingPagePairs(tocWithPage []model.TOCItem, tocWithPhysical []model.TOCItem, startPageIndex int) []tocPair {
	pairs := make([]tocPair, 0)
	for _, phy := range tocWithPhysical {
		if phy.PhysicalIndex == nil {
			continue
		}
		for _, pg := range tocWithPage {
			if pg.Page == nil {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(phy.Title), strings.TrimSpace(pg.Title)) && *phy.PhysicalIndex >= startPageIndex {
				pairs = append(pairs, tocPair{
					Page:          *pg.Page,
					PhysicalIndex: *phy.PhysicalIndex,
				})
			}
		}
	}
	return pairs
}

func calculatePageOffset(pairs []tocPair) (int, bool) {
	if len(pairs) == 0 {
		return 0, false
	}
	counts := map[int]int{}
	bestOffset := 0
	bestCount := 0
	for _, p := range pairs {
		diff := p.PhysicalIndex - p.Page
		counts[diff]++
		if counts[diff] > bestCount {
			bestCount = counts[diff]
			bestOffset = diff
		}
	}
	return bestOffset, true
}

func addPageOffsetToTOC(items []model.TOCItem, offset int) []model.TOCItem {
	out := make([]model.TOCItem, 0, len(items))
	for _, it := range items {
		c := it
		if c.Page != nil {
			v := *c.Page + offset
			c.PhysicalIndex = &v
			c.Page = nil
		}
		out = append(out, c)
	}
	return out
}

func processNonePageNumbers(ctx context.Context, client *llm.Client, tocItems []model.TOCItem, pages []model.Page, startIndex int, modelName string) []model.TOCItem {
	out := make([]model.TOCItem, len(tocItems))
	copy(out, tocItems)
	const maxConcurrent = 8
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	for i := range out {
		if out[i].PhysicalIndex != nil {
			continue
		}
		// Match Python defaults exactly:
		// prev defaults to (start_index - 1), next defaults to -1.
		// If next remains < prev, Python builds an empty range and skips fixing.
		prev := startIndex - 1
		next := -1
		for j := i - 1; j >= 0; j-- {
			if out[j].PhysicalIndex != nil {
				prev = *out[j].PhysicalIndex
				break
			}
		}
		for j := i + 1; j < len(out); j++ {
			if out[j].PhysicalIndex != nil {
				next = *out[j].PhysicalIndex
				break
			}
		}
		if next < prev {
			continue
		}
		wg.Add(1)
		go func(idx int, prevP, nextP int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			content := buildTaggedRangeText(pages, prevP, nextP, startIndex)
			itemCopy := out[idx]
			itemCopy.Page = nil
			updated, err := addPageNumberToTOC(ctx, client, content, []model.TOCItem{itemCopy}, modelName)
			if err != nil || len(updated) == 0 {
				return
			}
			if updated[0].PhysicalIndex != nil {
				v := *updated[0].PhysicalIndex
				out[idx].PhysicalIndex = &v
				out[idx].Page = nil
			}
		}(i, prev, next)
	}
	wg.Wait()
	return out
}

func processNoTOC(ctx context.Context, client *llm.Client, pages []model.Page, opt config.Options, startIndex int) ([]model.TOCItem, error) {
	chunks := buildTaggedChunks(pages, opt, effectiveChunkMaxTokens(opt), startIndex)
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no chunks generated")
	}
	return processNoTOCAttempt(ctx, client, pages, chunks, opt, startIndex)
}

func effectiveChunkMaxTokens(opt config.Options) int {
	maxTokens := opt.MaxTokenNumEachNode
	// Keep parity with Python behavior: use configured threshold directly.
	return maxTokens
}

func processNoTOCAttempt(ctx context.Context, client *llm.Client, pages []model.Page, chunks []string, opt config.Options, startIndex int) ([]model.TOCItem, error) {
	initialPrompt := `
    You are an expert in extracting hierarchical tree structure, your task is to generate the tree structure of the document.

    The structure variable is the numeric system which represents the index of the hierarchy section in the table of contents. For example, the first section has structure index 1, the first subsection has structure index 1.1, the second subsection has structure index 1.2, etc.

    For the title, you need to extract the original title from the text, only fix the space inconsistency.

    The provided text contains tags like <physical_index_X> and <physical_index_X> to indicate the start and end of page X. 

    For the physical_index, you need to extract the physical index of the start of the section from the text. Keep the <physical_index_X> format.

    The response should be in the following format. 
        [
            {
                "structure": <structure index, "x.x.x"> (string),
                "title": <title of the section, keep the original title>,
                "physical_index": "<physical_index_X> (keep the format)"
            },
            
        ],


    Directly return the final JSON structure. Do not output anything else.`
	firstOut, finishReason, err := client.ChatWithFinishReason(ctx, opt.Model, initialPrompt+"\nGiven text\n:"+chunks[0], nil)
	if err != nil {
		return nil, err
	}
	if finishReason != "" &&
		!strings.EqualFold(finishReason, "stop") &&
		!strings.EqualFold(finishReason, "finished") &&
		!strings.EqualFold(finishReason, "cached") {
		return nil, fmt.Errorf("generate_toc_init finish reason: %s", finishReason)
	}

	toc, err := parseTOCItems(firstOut)
	if err != nil {
		// Match Python extract_json behavior: malformed JSON degrades to {}
		// and processing continues instead of hard-failing immediately.
		toc = []model.TOCItem{}
	}

	for _, chunk := range chunks[1:] {
		continuePrompt := `
    You are an expert in extracting hierarchical tree structure.
    You are given a tree structure of the previous part and the text of the current part.
    Your task is to continue the tree structure from the previous part to include the current part.

    The structure variable is the numeric system which represents the index of the hierarchy section in the table of contents. For example, the first section has structure index 1, the first subsection has structure index 1.1, the second subsection has structure index 1.2, etc.

    For the title, you need to extract the original title from the text, only fix the space inconsistency.

    The provided text contains tags like <physical_index_X> and <physical_index_X> to indicate the start and end of page X. 
    
    For the physical_index, you need to extract the physical index of the start of the section from the text. Keep the <physical_index_X> format.

    The response should be in the following format. 
        [
            {
                "structure": <structure index, "x.x.x"> (string),
                "title": <title of the section, keep the original title>,
                "physical_index": "<physical_index_X> (keep the format)"
            },
            ...
        ]    

    Directly return the additional part of the final JSON structure. Do not output anything else.`
		historyPrompt := fmt.Sprintf("%s\nGiven text\n:%s\nPrevious tree structure\n:%s", continuePrompt, chunk, marshalTOCForContinue(toc))
		addOut, addFinishReason, callErr := client.ChatWithFinishReason(ctx, opt.Model, historyPrompt, nil)
		if callErr != nil {
			return nil, callErr
		}
		if addFinishReason != "" &&
			!strings.EqualFold(addFinishReason, "stop") &&
			!strings.EqualFold(addFinishReason, "finished") &&
			!strings.EqualFold(addFinishReason, "cached") {
			return nil, fmt.Errorf("generate_toc_continue finish reason: %s", addFinishReason)
		}
		add, parseErr := parseTOCItems(addOut)
		if parseErr != nil {
			// Python extract_json is tolerant and may return empty payloads on
			// malformed continuations; keep going instead of hard-failing.
			continue
		}
		if len(add) > 0 {
			toc = append(toc, add...)
		}
	}

	// Keep no-TOC behavior aligned with Python flow:
	// use tolerant parsing and downstream validation/fix steps
	// without extra Go-only topology rewrites here.

	if len(toc) == 0 {
		return nil, fmt.Errorf("empty toc generated")
	}

	return toc, nil
}

func buildTaggedChunks(pages []model.Page, opt config.Options, maxTokens int, startIndex int) []string {
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
		t := tokens.Count(opt.Model, tagged)
		lengths = append(lengths, t)
		total += t
	}
	if total <= maxTokens {
		return []string{strings.Join(parts, "")}
	}

	// Match Python page_list_to_group_text behavior:
	// split around average_tokens_per_part with 1-page overlap.
	expectedParts := int(math.Ceil(float64(total) / float64(maxTokens)))
	avgTokensPerPart := int(math.Ceil((float64(total)/float64(expectedParts) + float64(maxTokens)) / 2))
	overlapPages := 1

	chunks := make([]string, 0, expectedParts)
	currentStart := 0
	currentTokens := 0
	for i := 0; i < len(parts); i++ {
		if currentTokens+lengths[i] > avgTokensPerPart && i > currentStart {
			chunks = append(chunks, strings.Join(parts[currentStart:i], ""))
			nextStart := i - overlapPages
			if nextStart < 0 {
				nextStart = 0
			}
			currentStart = nextStart
			currentTokens = 0
			for j := currentStart; j < i; j++ {
				currentTokens += lengths[j]
			}
		}
		currentTokens += lengths[i]
	}
	if currentStart < len(parts) {
		chunks = append(chunks, strings.Join(parts[currentStart:], ""))
	}
	return chunks
}

func normalizeTOCItems(items []model.TOCItem, pageCount int, startIndex int) []model.TOCItem {
	out := make([]model.TOCItem, 0, len(items))
	for i := range items {
		item := items[i]
		item.Structure = strings.TrimSpace(item.Structure)
		item.Title = strings.TrimSpace(item.Title)
		if item.Title == "" {
			continue
		}
		if item.PhysicalIndex != nil {
			if *item.PhysicalIndex < startIndex || *item.PhysicalIndex > (pageCount+startIndex-1) {
				item.PhysicalIndex = nil
			}
		}
		out = append(out, item)
	}
	return out
}

func addPrefaceIfNeeded(items []model.TOCItem, startIndex int) []model.TOCItem {
	if len(items) == 0 || items[0].PhysicalIndex == nil {
		return items
	}
	if *items[0].PhysicalIndex <= startIndex {
		return items
	}
	p := startIndex
	preface := model.TOCItem{
		Structure:     "0",
		Title:         "Preface",
		PhysicalIndex: &p,
	}
	return append([]model.TOCItem{preface}, items...)
}

func validateAndTruncatePhysicalIndices(items []model.TOCItem, pageListLength int, startIndex int) []model.TOCItem {
	if len(items) == 0 {
		return items
	}
	maxAllowedPage := pageListLength + startIndex - 1
	out := make([]model.TOCItem, 0, len(items))
	for _, item := range items {
		c := item
		if c.PhysicalIndex != nil && *c.PhysicalIndex > maxAllowedPage {
			c.PhysicalIndex = nil
		}
		out = append(out, c)
	}
	return out
}

func retainItemsWithPhysicalIndex(items []model.TOCItem) []model.TOCItem {
	if len(items) == 0 {
		return items
	}
	out := make([]model.TOCItem, 0, len(items))
	for _, item := range items {
		if item.PhysicalIndex == nil {
			continue
		}
		out = append(out, item)
	}
	return out
}

type postProcessItem struct {
	Structure string
	Title     string
	Start     int
	End       int
}

func postProcessTOCItems(items []model.TOCItem, endPhysicalIndex int) []*model.Node {
	if len(items) == 0 {
		return nil
	}
	valid := make([]model.TOCItem, 0, len(items))
	for _, item := range items {
		if item.PhysicalIndex == nil {
			continue
		}
		valid = append(valid, item)
	}
	if len(valid) == 0 {
		return nil
	}

	processed := make([]postProcessItem, 0, len(items))
	for i := 0; i < len(valid); i++ {
		item := valid[i]
		start := *item.PhysicalIndex
		end := endPhysicalIndex
		if i < len(valid)-1 {
			next := *valid[i+1].PhysicalIndex
			if strings.EqualFold(strings.TrimSpace(valid[i+1].AppearStart), "yes") {
				end = next - 1
			} else {
				end = next
			}
		}
		processed = append(processed, postProcessItem{
			Structure: item.Structure,
			Title:     item.Title,
			Start:     start,
			End:       end,
		})
	}
	if len(processed) == 0 {
		return nil
	}
	tree := listToTreeFromTOC(processed)
	if len(tree) != 0 {
		return tree
	}
	flat := make([]*model.Node, 0, len(processed))
	for _, item := range processed {
		s, e := item.Start, item.End
		flat = append(flat, &model.Node{
			Title:      item.Title,
			StartIndex: &s,
			EndIndex:   &e,
		})
	}
	return flat
}

func listToTreeFromTOC(items []postProcessItem) []*model.Node {
	nodes := make(map[string]*model.Node, len(items))
	roots := make([]*model.Node, 0, len(items))
	for _, item := range items {
		key := strings.TrimSpace(item.Structure)
		node := &model.Node{
			Title:      item.Title,
			StartIndex: &item.Start,
			EndIndex:   &item.End,
			Nodes:      []*model.Node{},
		}
		nodes[key] = node
		parentKey := parentStructure(key)
		parent, ok := nodes[parentKey]
		if parentKey == "" || !ok {
			roots = append(roots, node)
			continue
		}
		parent.Nodes = append(parent.Nodes, node)
	}

	var clean func(*model.Node)
	clean = func(n *model.Node) {
		if n == nil {
			return
		}
		if len(n.Nodes) == 0 {
			n.Nodes = nil
			return
		}
		for _, ch := range n.Nodes {
			clean(ch)
		}
	}
	for _, r := range roots {
		clean(r)
	}
	return roots
}

func parseTOCItems(raw string) ([]model.TOCItem, error) {
	clean := normalizeTOCJSON(raw)
	// Mirror Python extract_json behavior: flatten line breaks and normalize
	// whitespace before decoding.
	clean = strings.ReplaceAll(clean, "\n", " ")
	clean = strings.ReplaceAll(clean, "\r", " ")
	clean = strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return ' '
		}
		return r
	}, clean)
	clean = strings.Join(strings.Fields(clean), " ")
	if idx := strings.IndexAny(clean, "[{"); idx > 0 {
		clean = clean[idx:]
	}

	decoded, err := decodeTOCJSON(clean)
	if err != nil {
		// Python extract_json is permissive; salvage complete item objects from
		// partial/truncated payloads when full JSON decode fails.
		if partial := extractPartialTOCMaps(clean); len(partial) > 0 {
			return tocItemsFromMaps(partial), nil
		}
		sample := clean
		if len(sample) > 220 {
			sample = sample[:220]
		}
		return nil, fmt.Errorf("parse toc json: %w; sample=%q", err, sample)
	}

	arr := tocMapsFromDecoded(decoded)
	if len(arr) == 0 {
		return nil, fmt.Errorf("empty toc payload")
	}

	return tocItemsFromMaps(arr), nil
}

func normalizeTOCJSON(raw string) string {
	clean := getJSONContent(util.ExtractJSONString(raw))
	if clean == "" {
		clean = getJSONContent(raw)
	}
	if clean == "" {
		clean = strings.TrimSpace(raw)
	}
	clean = strings.ReplaceAll(clean, "None", "null")
	clean = tocTrailingCommaRE.ReplaceAllString(clean, "$1")
	return strings.TrimSpace(clean)
}

func decodeTOCJSON(clean string) (any, error) {
	var decoded any
	dec := json.NewDecoder(strings.NewReader(clean))
	if err := dec.Decode(&decoded); err == nil {
		return decoded, nil
	}

	recovered := recoverTOCJSON(clean)
	if recovered == "" || recovered == clean {
		return nil, fmt.Errorf("decode failed")
	}
	dec = json.NewDecoder(strings.NewReader(recovered))
	if err := dec.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func recoverTOCJSON(clean string) string {
	if clean == "" {
		return ""
	}
	if idx := strings.IndexAny(clean, "[{"); idx > 0 {
		clean = clean[idx:]
	}
	stack := make([]byte, 0, 32)
	inString := false
	escaped := false
	stringStart := -1
	for i := 0; i < len(clean); i++ {
		ch := clean[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
				stringStart = -1
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
			stringStart = i
		case '{', '[':
			stack = append(stack, ch)
		case '}':
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
			}
		case ']':
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if inString && stringStart >= 0 {
		clean = clean[:stringStart]
	}
	clean = strings.TrimSpace(clean)
	for strings.HasSuffix(clean, ",") {
		clean = strings.TrimSpace(strings.TrimSuffix(clean, ","))
	}
	clean = tocTrailingCommaRE.ReplaceAllString(clean, "$1")
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == '{' {
			clean += "}"
		} else {
			clean += "]"
		}
	}
	return strings.TrimSpace(clean)
}

func tocMapsFromDecoded(decoded any) []map[string]any {
	arr := make([]map[string]any, 0)
	switch t := decoded.(type) {
	case []any:
		for _, it := range t {
			if m, ok := it.(map[string]any); ok {
				arr = append(arr, m)
			}
		}
	case map[string]any:
		if tocRaw, ok := t["table_of_contents"]; ok {
			switch toc := tocRaw.(type) {
			case []any:
				for _, it := range toc {
					if m, ok := it.(map[string]any); ok {
						arr = append(arr, m)
					}
				}
			case map[string]any:
				arr = append(arr, toc)
			}
		} else {
			arr = append(arr, t)
		}
	}
	return arr
}

func extractPartialTOCMaps(clean string) []map[string]any {
	src := strings.TrimSpace(clean)
	if src == "" {
		return nil
	}

	// Prefer parsing the items array body when present.
	if key := strings.Index(src, `"table_of_contents"`); key != -1 {
		if open := strings.Index(src[key:], "["); open != -1 {
			src = src[key+open+1:]
		}
	} else {
		src = strings.TrimPrefix(src, "[")
	}

	out := make([]map[string]any, 0)
	inString := false
	escaped := false
	depth := 0
	start := -1
	for i := 0; i < len(src); i++ {
		ch := src[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				piece := strings.TrimSpace(src[start : i+1])
				var m map[string]any
				if err := json.Unmarshal([]byte(piece), &m); err == nil {
					out = append(out, m)
				}
				start = -1
			}
		}
	}
	return out
}

func tocItemsFromMaps(arr []map[string]any) []model.TOCItem {
	out := make([]model.TOCItem, 0, len(arr))
	for _, row := range arr {
		item := model.TOCItem{
			Structure: structureAny(row["structure"]),
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
	return out
}

func structureAny(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case float64:
		if math.Trunc(t) == t {
			return strconv.Itoa(int(t))
		}
		return strings.TrimSpace(strconv.FormatFloat(t, 'f', -1, 64))
	case int:
		return strconv.Itoa(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
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
	lastPhysicalIndex := 0
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].PhysicalIndex != nil {
			lastPhysicalIndex = *items[i].PhysicalIndex
			break
		}
	}
	// Mirror Python early-return behavior when physical indices are too sparse.
	if lastPhysicalIndex == 0 || float64(lastPhysicalIndex) < float64(len(pages))/2.0 {
		return 0, []int{}
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
	const maxConcurrentFix = 8
	sem := make(chan struct{}, maxConcurrentFix)
	for attempt := 0; attempt < maxAttempts && len(currentIncorrect) > 0; attempt++ {
		incorrectSet := make(map[int]struct{}, len(currentIncorrect))
		for _, idx := range currentIncorrect {
			incorrectSet[idx] = struct{}{}
		}
		var wg sync.WaitGroup
		for _, badIdx := range currentIncorrect {
			if badIdx < 0 || badIdx >= len(current) {
				continue
			}
			wg.Add(1)
			go func(badIdx int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				// Match Python fix_incorrect_toc: default prev_correct is start_index - 1.
				prev := startIndex - 1
				next := len(pages) + startIndex - 1
				for i := badIdx - 1; i >= 0; i-- {
					if _, isIncorrect := incorrectSet[i]; isIncorrect {
						continue
					}
					if current[i].PhysicalIndex != nil {
						prev = *current[i].PhysicalIndex
						break
					}
				}
				for i := badIdx + 1; i < len(current); i++ {
					if _, isIncorrect := incorrectSet[i]; isIncorrect {
						continue
					}
					if current[i].PhysicalIndex != nil {
						next = *current[i].PhysicalIndex
						break
					}
				}
				content := buildTaggedRangeText(pages, prev, next, startIndex)
				fixedIdx, err := singleTOCItemIndexFixer(ctx, client, current[badIdx].Title, content, modelName)
				if err != nil {
					return
				}
				if fixedIdx >= startIndex && fixedIdx <= len(pages)+startIndex-1 {
					localIdx := fixedIdx - startIndex
					if localIdx >= 0 && localIdx < len(pages) {
						ok, checkErr := checkTitleAppearance(ctx, client, current[badIdx].Title, pages[localIdx].Text, modelName)
						if checkErr == nil && ok {
							v := fixedIdx
							current[badIdx].PhysicalIndex = &v
						}
					}
				}
			}(badIdx)
		}
		wg.Wait()
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
	prompt := fmt.Sprintf(`
Your job is to check if the given section appears or starts in the given page_text.

Note: do fuzzy matching, ignore any space inconsistency in the page_text.

The given section title is %s.
The given page_text is %s.

Reply format:
{
  "thinking": <why do you think the section appears or starts in the page_text>,
  "answer": "yes or no" (yes if the section appears or starts in the page_text, no otherwise)
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
	prompt := fmt.Sprintf(`
You will be given the current section title and the current page_text.
Your job is to check if the current section starts in the beginning of the given page_text.
If there are other contents before the current section title, then the current section does not start in the beginning of the given page_text.
If the current section title is the first content in the given page_text, then the current section starts in the beginning of the given page_text.

Note: do fuzzy matching, ignore any space inconsistency in the page_text.

The given section title is %s.
The given page_text is %s.

reply format:
{
  "thinking": <why do you think the section appears or starts in the page_text>,
  "start_begin": "yes or no" (yes if the section starts in the beginning of the page_text, no otherwise)
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
	// Match Python semantics: when prev_correct > next_correct the range is empty.
	if startPage > endPage {
		return ""
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
		pageSpan := end - start
		if pageSpan > opt.MaxPageNumEachNode && tokenNum >= opt.MaxTokenNumEachNode {
			subPages := pages[start-1 : end]
			subItems, err := metaProcessTOC(ctx, client, subPages, tocCheckResult{PageIndexGivenInTOC: "no"}, opt, start)
			if err == nil && len(subItems) > 0 {
				subItems = normalizeTOCItems(subItems, len(subPages), start)
				subItems = validateAndTruncatePhysicalIndices(subItems, len(subPages), start)
				addAppearStart(ctx, client, pages, subItems, opt.Model, 1)

				validSubItems := make([]model.TOCItem, 0, len(subItems))
				for _, item := range subItems {
					if item.PhysicalIndex != nil {
						validSubItems = append(validSubItems, item)
					}
				}

				childrenItems := validSubItems
				if len(validSubItems) > 0 && strings.EqualFold(strings.TrimSpace(validSubItems[0].Title), strings.TrimSpace(node.Title)) {
					if len(validSubItems) > 1 {
						childrenItems = validSubItems[1:]
						next := *validSubItems[1].PhysicalIndex
						node.EndIndex = &next
					} else {
						childrenItems = nil
					}
				} else if len(validSubItems) > 0 {
					next := *validSubItems[0].PhysicalIndex
					node.EndIndex = &next
				}

				children := postProcessTOCItems(childrenItems, end)
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

func marshalTOCForContinue(items []model.TOCItem) string {
	type continueRow struct {
		Structure     string `json:"structure,omitempty"`
		Title         string `json:"title,omitempty"`
		PhysicalIndex string `json:"physical_index,omitempty"`
	}
	out := make([]continueRow, 0, len(items))
	for _, it := range items {
		row := continueRow{}
		if strings.TrimSpace(it.Structure) != "" {
			row.Structure = strings.TrimSpace(it.Structure)
		}
		if strings.TrimSpace(it.Title) != "" {
			row.Title = strings.TrimSpace(it.Title)
		}
		if it.PhysicalIndex != nil {
			row.PhysicalIndex = fmt.Sprintf("<physical_index_%d>", *it.PhysicalIndex)
		}
		out = append(out, row)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return marshalPretty(items)
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

			prompt := fmt.Sprintf(`You are given a part of a document, your task is to generate a description of the partial document about what are main points covered in the partial document.

    Partial Document Text: %s
    
    Directly return the description, do not include any other text.
    `, n.Text)
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
	prompt := fmt.Sprintf(`Your are an expert in generating descriptions for a document.
    You are given a structure of a document. Your task is to generate a one-sentence description for the document, which makes it easy to distinguish the document from other documents.
        
    Document Structure: %v
    
    Directly return the description, do not include any other text.
    `, structure)
	resp, err := client.Chat(ctx, modelName, prompt, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp), nil
}
