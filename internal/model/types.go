package model

type Node struct {
	Title         string  `json:"title"`
	NodeID        string  `json:"node_id,omitempty"`
	StartIndex    *int    `json:"start_index,omitempty"`
	EndIndex      *int    `json:"end_index,omitempty"`
	Summary       string  `json:"summary,omitempty"`
	PrefixSummary string  `json:"prefix_summary,omitempty"`
	Text          string  `json:"text,omitempty"`
	LineNum       int     `json:"line_num,omitempty"`
	Nodes         []*Node `json:"nodes,omitempty"`
}

type TOCItem struct {
	Structure     string `json:"structure,omitempty"`
	Title         string `json:"title"`
	Page          *int   `json:"page,omitempty"`
	PhysicalIndex *int   `json:"physical_index,omitempty"`
	AppearStart   string `json:"appear_start,omitempty"`
}

type Result struct {
	DocName        string  `json:"doc_name"`
	DocDescription string  `json:"doc_description,omitempty"`
	Structure      []*Node `json:"structure"`
}

type Page struct {
	Text       string
	TokenCount int
}
