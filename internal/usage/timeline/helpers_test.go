package timeline

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Synthetic identifiers shared by the tests. They match the shared fixtures
// in internal/usage/testdata.
const (
	fixtureSession  = "11111111-1111-4111-8111-111111111111"
	fixtureAgent    = "a1b2c3"
	fixtureModel    = "model-large"
	fixtureRootFile = "session.jsonl"
	fixtureSubFile  = "agent-a1b2c3.jsonl"
	testdataDir     = "../testdata"
)

// maxTestPages stops a paging loop that fails to make progress.
const maxTestPages = 10_000

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(os.DirFS(testdataDir), name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// obj is one JSON object of a synthetic transcript.
type obj = map[string]any

// transcript encodes each value as one JSONL line. A string value is written
// as is, which lets a test place a line that is not valid JSON.
func transcript(t testing.TB, lines ...any) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, ln := range lines {
		if raw, ok := ln.(string); ok {
			out.WriteString(raw)
			out.WriteByte('\n')
			continue
		}
		encoded, err := json.Marshal(ln)
		if err != nil {
			t.Fatalf("encode transcript line: %v", err)
		}
		out.Write(encoded)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func promptLine(uuid, at, text string) obj {
	return obj{"type": "user", "uuid": uuid, "timestamp": at, "message": obj{"role": "user", "content": text}}
}

func assistantLine(uuid, at, messageID string, blocks ...obj) obj {
	return obj{
		"type": "assistant", "uuid": uuid, "timestamp": at,
		"message": obj{
			"id": messageID, "model": fixtureModel, "content": blocks,
			"usage": obj{
				"input_tokens": 1, "output_tokens": 2,
				"cache_read_input_tokens": 3, "cache_creation_input_tokens": 4,
			},
		},
	}
}

func toolUse(id, name string, input any) obj {
	return obj{"type": "tool_use", "id": id, "name": name, "input": input}
}

func resultLine(uuid, at string, blocks ...obj) obj {
	return obj{"type": "user", "uuid": uuid, "timestamp": at, "message": obj{"role": "user", "content": blocks}}
}

func toolResult(useID string, content any) obj {
	return obj{"type": "tool_result", "tool_use_id": useID, "content": content, "is_error": false}
}

// mustRead reads one page and fails the test on an error.
func mustRead(t testing.TB, data []byte, req usage.TimelineRequest) usage.TimelinePage {
	t.Helper()
	page, err := Read(bytes.NewReader(data), req)
	if err != nil {
		t.Fatalf("Read(%+v): %v", req, err)
	}
	return page
}

// readAll follows NextCursor until the stream ends and returns the items of
// all pages, the number of pages and the sum of their Skipped counts.
func readAll(t testing.TB, data []byte, req usage.TimelineRequest) (items []usage.TimelineItem, pages, skipped int) {
	t.Helper()
	items = []usage.TimelineItem{}
	for pages < maxTestPages {
		page := mustRead(t, data, req)
		pages++
		if page.Source != usage.SourceAvailable {
			t.Fatalf("page %d: Source = %q; want %q", pages, page.Source, usage.SourceAvailable)
		}
		if page.Items == nil {
			t.Fatalf("page %d: Items is nil; want an empty slice", pages)
		}
		items = append(items, page.Items...)
		skipped += page.Skipped
		if page.NextCursor == "" {
			return items, pages, skipped
		}
		if len(page.Items) == 0 {
			t.Fatalf("page %d: a page with a NextCursor holds no items", pages)
		}
		req.Cursor = page.NextCursor
	}
	t.Fatalf("paging did not end after %d pages", maxTestPages)
	return nil, 0, 0
}

// row is the part of a timeline item the sequence tests compare. Pointer
// fields are flattened so that a mismatch prints readable values.
type row struct {
	ID       string
	Kind     usage.TimelineKind
	AgentID  string
	Text     string
	Label    string
	Model    string
	Tokens   string
	Tool     string
	Category usage.ToolCategory
	UseID    string
	IsError  bool
	Duration string
	Hook     string
}

const noValue = "-"

func rowOf(it usage.TimelineItem) row {
	r := row{
		ID: it.ID, Kind: it.Kind, AgentID: it.AgentID, Text: it.Text, Label: it.Label, Model: it.Model,
		Tokens: noValue, Duration: noValue, Hook: noValue,
	}
	if it.Tokens != nil {
		r.Tokens = tokens(it.Tokens.Input, it.Tokens.Output, it.Tokens.CacheRead, it.Tokens.CacheCreate)
	}
	if it.Tool != nil {
		r.Tool, r.Category, r.UseID, r.IsError = it.Tool.Name, it.Tool.Category, it.Tool.UseID, it.Tool.IsError
		if it.Tool.DurationMs != nil {
			r.Duration = itoa(*it.Tool.DurationMs)
		}
	}
	if it.Hook != nil {
		r.Hook = strings.Join([]string{
			it.Hook.Event, it.Hook.Name, string(it.Hook.Status), itoa(int64(it.Hook.ExitCode)), itoa(it.Hook.DurationMs),
		}, "|")
	}
	return r
}

func rowsOf(items []usage.TimelineItem) []row {
	rows := make([]row, 0, len(items))
	for _, it := range items {
		rows = append(rows, rowOf(it))
	}
	return rows
}

func tokens(input, output, cacheRead, cacheCreate int64) string {
	return strings.Join([]string{itoa(input), itoa(output), itoa(cacheRead), itoa(cacheCreate)}, "/")
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func compareRows(t *testing.T, got, want []row) {
	t.Helper()
	for i := 0; i < len(got) || i < len(want); i++ {
		switch {
		case i >= len(got):
			t.Errorf("item %d is missing; want %+v", i, want[i])
		case i >= len(want):
			t.Errorf("item %d is unexpected: %+v", i, got[i])
		case got[i] != want[i]:
			t.Errorf("item %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}
