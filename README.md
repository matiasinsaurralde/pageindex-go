# pageindex-go

[![build](https://img.shields.io/github/actions/workflow/status/matiasinsaurralde/pageindex-go/ci.yml?branch=main&label=build)](https://github.com/matiasinsaurralde/pageindex-go/actions/workflows/ci.yml)
[![tests](https://img.shields.io/github/actions/workflow/status/matiasinsaurralde/pageindex-go/ci.yml?branch=main&label=tests)](https://github.com/matiasinsaurralde/pageindex-go/actions/workflows/ci.yml)

Go rewrite of PageIndex for generating a hierarchical, reasoning-friendly tree index from PDF documents.

> [!IMPORTANT]
> **Attribution and derivation notice**
>
> This project is a rewrite/derivative work inspired by and partially derived from the original [VectifyAI/PageIndex](https://github.com/VectifyAI/PageIndex) project.
>
> - Upstream project: `VectifyAI/PageIndex`
> - Upstream license: **MIT License**
> - This repository keeps its own [`LICENSE`](./LICENSE) and includes upstream/third-party notices in [`THIRD_PARTY_LICENSES.md`](./THIRD_PARTY_LICENSES.md)

## What this project does

- Parses PDF input
- Builds a tree-like document structure (table-of-contents style)
- Uses LLM-based reasoning to enrich structure and summaries
- Outputs JSON suitable for downstream retrieval/analysis workflows

## Requirements

- Go `1.24.x` (see `go.mod`)
- OpenAI-compatible API key via:
  - `CHATGPT_API_KEY`, or
  - `OPENAI_API_KEY`

## Installation

```bash
go mod download
```

## Quickstart (CLI)

1. Create a `.env` file in the repository root:

```bash
CHATGPT_API_KEY=your_api_key_here
```

2. Run PageIndex on a PDF:

```bash
go run ./cmd/pageindex --pdf-path /path/to/document.pdf
```

3. Save output to a file:

```bash
go run ./cmd/pageindex \
  --pdf-path /path/to/document.pdf \
  --config pageindex/config.yaml \
  --output ./out/result.json
```

Optional flags:

- `--model` override model from config
- `--config` path to YAML config (default: `pageindex/config.yaml`)
- `--output` output JSON path (prints to stdout if omitted)

## Configuration

Default settings live in `pageindex/config.yaml`.

```yaml
model: "gpt-4o-2024-11-20"
toc_check_page_num: 20
toc_attempts: 2
max_page_num_each_node: 10
max_token_num_each_node: 20000
if_add_node_id: "yes"
if_add_node_summary: "yes"
if_add_doc_description: "no"
if_add_node_text: "no"
```

## Library usage

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/matiasinsaurralde/go-pageindex/pkg/pageindex"
)

func main() {
    opts, err := pageindex.LoadOptions("pageindex/config.yaml")
    if err != nil {
        panic(err)
    }

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
    defer cancel()

    result, err := pageindex.PageIndexPDFPath(ctx, "/path/to/document.pdf", opts)
    if err != nil {
        panic(err)
    }

    fmt.Printf("doc: %s\n", result.DocName)
}
```

## Development

This repository includes a `Makefile` and CI workflow to keep local and CI checks aligned.

```bash
make test
make build
make lint
make ci
```

CI runs these same steps in `.github/workflows/ci.yml`.

## License

- This repository is licensed under the MIT License: see [`LICENSE`](./LICENSE).
- This project is derived from third-party open source work, including the original `VectifyAI/PageIndex` (MIT): see [`THIRD_PARTY_LICENSES.md`](./THIRD_PARTY_LICENSES.md).
