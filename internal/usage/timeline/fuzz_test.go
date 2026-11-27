package timeline

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// fuzzMaxLine keeps the oversized-line path within reach of small inputs.
const fuzzMaxLine = 4 << 10

// fuzzPages reads data to the end with the given page size and checks what
// must hold for every page, whatever the bytes are.
func fuzzPages(t *testing.T, data []byte, limit int) (items []usage.TimelineItem, skipped int) {
	t.Helper()
	req := usage.TimelineRequest{Limit: limit}
	lines := bytes.Count(data, []byte("\n")) + 1
	lastOffset := int64(-1)
	for page := 0; ; page++ {
		if page > lines {
			t.Fatalf("limit %d: more pages than lines (%d)", limit, lines)
		}
		got, err := read(bytes.NewReader(data), req, fuzzMaxLine)
		if err != nil {
			t.Fatalf("limit %d, page %d: %v", limit, page, err)
		}
		if got.Source != usage.SourceAvailable || got.Items == nil || got.Skipped < 0 {
			t.Fatalf("limit %d, page %d: Source %q, Items nil %v, Skipped %d", limit, page, got.Source, got.Items == nil, got.Skipped)
		}
		if _, err := json.Marshal(got); err != nil {
			t.Fatalf("limit %d, page %d: page does not encode: %v", limit, page, err)
		}
		for _, it := range got.Items {
			checkFuzzItem(t, it)
		}
		items = append(items, got.Items...)
		skipped += got.Skipped
		if got.NextCursor == "" {
			return items, skipped
		}
		if len(got.Items) == 0 {
			t.Fatalf("limit %d, page %d: a cursor follows a page without items", limit, page)
		}
		offset, st, err := decodeCursor(got.NextCursor)
		if err != nil {
			t.Fatalf("limit %d, page %d: the reader's own cursor is refused: %v", limit, page, err)
		}
		if offset <= lastOffset || len(st.open) > maxOpenCalls {
			t.Fatalf("limit %d, page %d: cursor offset %d after %d, %d open calls", limit, page, offset, lastOffset, len(st.open))
		}
		lastOffset = offset
		req.Cursor = got.NextCursor
	}
}

func checkFuzzItem(t *testing.T, it usage.TimelineItem) {
	t.Helper()
	texts := []string{it.Text, it.Label, it.Model}
	if it.Tool != nil {
		texts = append(texts, it.Tool.Input, it.Tool.Name, it.Tool.UseID)
	}
	if it.Hook != nil {
		texts = append(texts, it.Hook.Event, it.Hook.Name)
	}
	for _, text := range texts {
		if len(text) > usage.MaxTimelineText {
			t.Fatalf("%s: a text field is %d bytes; the cap is %d", it.ID, len(text), usage.MaxTimelineText)
		}
		if !utf8.ValidString(text) {
			t.Fatalf("%s: a text field is not valid UTF-8", it.ID)
		}
	}
	if it.ID == "" || it.Kind == "" || it.Masked < 0 {
		t.Fatalf("item without ID or kind, or with a negative mask count: %+v", it)
	}
	if it.Kind == usage.ItemThinking && it.Text != "" {
		t.Fatalf("%s: a thinking item carries text", it.ID)
	}
}

func FuzzRead(f *testing.F) {
	f.Add(readFixture(f, fixtureRootFile), uint8(1))
	f.Add(readFixture(f, fixtureSubFile), uint8(2))
	f.Add([]byte(""), uint8(0))
	f.Add([]byte("\n\n{}\nnull\n[]\n\"x\"\n{"), uint8(3))
	f.Add([]byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":[{"type":"text","text":"a"}]}]},"toolUseResult":{"durationMs":1e400}}`), uint8(1))
	f.Add([]byte(`{"type":"assistant","uuid":"a","timestamp":"2026-03-01T09:00:00Z","message":{"id":"m","content":[{"type":"thinking"},{"type":"tool_use","id":"t","name":"mcp__a__b","input":{"k":[1,{"`+"to"+"ken"+`":{"x":"y"}}]}}],"usage":{"output_tokens_details":null}}}`+"\n"+
		`{"type":"user","uuid":"r","timestamp":"2026-03-01T09:00:01Z","message":{"content":[{"type":"tool_result","tool_use_id":"t","content":"ok"}]}}`), uint8(1))
	f.Add([]byte(`{"type":"attachment","attachment":{"type":"hook_blocking_error","exitCode":-1.5e308,"durationMs":1e308}}`+"\n"+
		`{"type":"system","subtype":"turn_duration","durationMs":-0.0}`+"\n"+
		`{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":["a"]}}`), uint8(5))
	f.Add([]byte(strings.Repeat(`{"type":"user","uuid":"p","message":{"content":"`+strings.Repeat("y", 300)+`"}}`+"\n", 20)), uint8(4))
	f.Add([]byte(`{"type":"user","timestamp":"9999-12-31T23:59:59.999999999+23:59","message":{"content":"\ud800\u0000 \xff"}}`), uint8(1))

	f.Fuzz(func(t *testing.T, data []byte, limit uint8) {
		full, fullSkipped := fuzzPages(t, data, usage.MaxPageSize)
		paged, pagedSkipped := fuzzPages(t, data, int(limit%8)+1)
		// However the stream is cut into pages, the items are the same.
		if !reflect.DeepEqual(paged, full) || pagedSkipped != fullSkipped {
			t.Fatalf("paged read (%d items, %d skipped) differs from the full read (%d items, %d skipped)",
				len(paged), pagedSkipped, len(full), fullSkipped)
		}
		seen := make(map[string]bool, len(full))
		for _, it := range full {
			// Lines may repeat a uuid in arbitrary input; the offset-based
			// fallback IDs never collide with each other.
			if strings.HasPrefix(it.ID, "@") && seen[it.ID] {
				t.Fatalf("offset-based ID %q is used twice", it.ID)
			}
			seen[it.ID] = true
		}
	})
}

func FuzzReadCursor(f *testing.F) {
	data := readFixture(f, fixtureRootFile)
	valid, err := encodeCursor(int64(bytes.IndexByte(data, '\n')+1), state{
		open: []openCall{{ID: "toolu_01", Name: usage.ToolNameBash, Category: usage.ToolBuiltin, AtMs: 1}},
		call: callProgress{ID: "msg_01", Usage: true},
	})
	if err != nil {
		f.Fatalf("encodeCursor: %v", err)
	}
	f.Add(valid)
	f.Add("")
	f.Add("AAAA")
	f.Add(encodeRaw([]byte(`{"v":1,"o":5}`)))
	f.Add(encodeRaw([]byte(`{"v":1,"o":0,"c":[{"i":"a","n":"b","k":"mcp","t":9}],"m":{"i":"x","u":true,"h":true}}`)))
	f.Add(encodeRaw([]byte(`{"v":1,"o":1e3}`)))

	f.Fuzz(func(t *testing.T, cursor string) {
		page, err := Read(bytes.NewReader(data), usage.TimelineRequest{SessionID: fixtureSession, Cursor: cursor, Limit: 3})
		if err != nil {
			if !errors.Is(err, usage.ErrBadRequest) {
				t.Fatalf("err = %v; a cursor can only be a bad request", err)
			}
			return
		}
		if page.Source != usage.SourceAvailable || page.Items == nil {
			t.Fatalf("accepted cursor gives Source %q, Items nil %v", page.Source, page.Items == nil)
		}
		for _, it := range page.Items {
			checkFuzzItem(t, it)
		}
	})
}
