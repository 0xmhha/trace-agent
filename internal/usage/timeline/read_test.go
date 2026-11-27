package timeline

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// rootRows is the timeline of testdata/session.jsonl as its README describes
// it: 28 lines, one of them broken, one copied from another session.
func rootRows() []row {
	hook := func(id, event, name string, status usage.HookStatus, exit, ms int64) row {
		return row{
			ID: id, Kind: usage.ItemHook, Tokens: noValue, Duration: noValue,
			Hook: strings.Join([]string{event, name, string(status), itoa(exit), itoa(ms)}, "|"),
		}
	}
	call := func(id, tok, name string, category usage.ToolCategory, useID string) row {
		return row{
			ID: id, Kind: usage.ItemToolCall, Model: fixtureModel, Tokens: tok,
			Tool: name, Category: category, UseID: useID, Duration: noValue, Hook: noValue,
		}
	}
	result := func(id, text, name string, category usage.ToolCategory, useID string, isError bool, ms int64) row {
		return row{
			ID: id, Kind: usage.ItemToolResult, Text: text, Tokens: noValue,
			Tool: name, Category: category, UseID: useID, IsError: isError, Duration: itoa(ms), Hook: noValue,
		}
	}
	plain := func(id string, kind usage.TimelineKind, text, label string) row {
		return row{ID: id, Kind: kind, Text: text, Label: label, Tokens: noValue, Duration: noValue, Hook: noValue}
	}
	system := func(id, label, structured string) row {
		r := plain(id, usage.ItemSystem, "", label)
		r.System = structured
		return r
	}
	const (
		mcpSearch = "mcp__issues__search"
		mcpStatus = "mcp__plugin_devkit_chain__status"
	)
	firstCall := call("u-a1-2:0", noValue, usage.ToolNameBash, usage.ToolBuiltin, "toolu_01")
	firstCall.Model = ""
	return []row{
		hook("u-hook-1:0", "SessionStart", "SessionStart:startup", usage.HookSuccess, 0, 120),
		plain("u-prompt-1:0", usage.ItemPrompt, "Find why the build fails. The log is in build.log.", ""),
		{ID: "u-a1-0:0", Kind: usage.ItemThinking, Model: fixtureModel, Tokens: tokens(0, 150, 0, 0), Duration: noValue, Hook: noValue},
		{ID: "u-a1-1:0", Kind: usage.ItemAssistant, Text: "I will check the log first.", Model: fixtureModel, Tokens: tokens(12, 420, 48000, 2100), Duration: noValue, Hook: noValue},
		firstCall,
		hook("u-hook-2:0", "PreToolUse", "PreToolUse:Bash", usage.HookSuccess, 0, 31),
		result("u-r1:0", "error: cannot find module config", usage.ToolNameBash, usage.ToolBuiltin, "toolu_01", true, 2000),
		call("u-a2:0", tokens(8, 610, 50100, 300), mcpSearch, usage.ToolMCP, "toolu_02"),
		result("u-r2:0", "2 related issues", mcpSearch, usage.ToolMCP, "toolu_02", false, 2000),
		call("u-a3:0", tokens(5, 200, 51000, 100), usage.ToolNameSkill, usage.ToolSkill, "toolu_03"),
		result("u-r3:0", "skill loaded", usage.ToolNameSkill, usage.ToolSkill, "toolu_03", false, 1000),
		call("u-a4:0", tokens(6, 150, 51500, 80), mcpStatus, usage.ToolMCP, "toolu_04"),
		hook("u-hook-3:0", "PreToolUse", "PreToolUse:"+mcpStatus, usage.HookError, 1, 900),
		result("u-r4:0", "chain is healthy", mcpStatus, usage.ToolMCP, "toolu_04", false, 2000),
		call("u-a5:0", tokens(4, 90, 52000, 60), usage.ToolNameAgent, usage.ToolAgent, "toolu_05"),
		result("u-r5:0", "3 files import config", usage.ToolNameAgent, usage.ToolAgent, "toolu_05", false, 40000),
		{ID: "u-a6:0", Kind: usage.ItemAssistant, Text: "The import path of config changed. I fixed it.", Model: fixtureModel, Tokens: tokens(3, 300, 52500, 40), Duration: noValue, Hook: noValue},
		system("u-turn-1:0", "Turn ended · 70.0 s", systemRow(usage.SystemTurnEnd, "70000", "")),
		hook("u-hook-4:0", "Stop", "Stop", usage.HookSuccess, 0, 40),
		system("u-compact-1:0", "Context compacted (auto)", systemRow(usage.SystemCompaction, noValue, "auto")),
		plain("u-prompt-2:0", usage.ItemPrompt, "Thanks.", ""),
	}
}

func TestReadRootFixture(t *testing.T) {
	data := readFixture(t, fixtureRootFile)
	page := mustRead(t, data, usage.TimelineRequest{SessionID: fixtureSession, Limit: usage.MaxPageSize})

	compareRows(t, rowsOf(page.Items), rootRows())
	if page.Skipped != 1 {
		t.Errorf("Skipped = %d; want 1 (the broken line)", page.Skipped)
	}
	if page.Source != usage.SourceAvailable {
		t.Errorf("Source = %q; want %q", page.Source, usage.SourceAvailable)
	}
	if page.NextCursor != "" {
		t.Errorf("NextCursor = %q; want empty at the end of the file", page.NextCursor)
	}

	const wantInput = "{\n  \"command\": \"tail -n 40 build.log\",\n  \"description\": \"Show the end of the build log\"\n}"
	for _, it := range page.Items {
		switch {
		case it.ID == "u-a1-2:0" && it.Tool.Input != wantInput:
			t.Errorf("tool input = %q; want %q", it.Tool.Input, wantInput)
		case it.ID == "u-a4:0" && it.Tool.Input != "{}":
			t.Errorf("empty tool input = %q; want {}", it.Tool.Input)
		case it.Kind == usage.ItemToolResult && it.Tool.Input != "":
			t.Errorf("%s: a result carries input %q", it.ID, it.Tool.Input)
		case it.Kind == usage.ItemThinking && it.Text != "":
			t.Errorf("%s: a thinking item carries text", it.ID)
		}
		if it.Masked != 0 || it.Truncated {
			t.Errorf("%s: Masked = %d, Truncated = %v; the fixture holds nothing to mask or cut", it.ID, it.Masked, it.Truncated)
		}
	}
	if first := page.Items[0].At.Format("2006-01-02T15:04:05.000Z07:00"); first != "2026-03-01T09:00:00.000Z" {
		t.Errorf("first item At = %s; want 2026-03-01T09:00:00.000Z", first)
	}
}

// Without a session ID in the request the reader cannot tell a copied line
// from the stream's own, so the last line of the fixture is shown too.
func TestReadWithoutSessionFilter(t *testing.T) {
	page := mustRead(t, readFixture(t, fixtureRootFile), usage.TimelineRequest{Limit: usage.MaxPageSize})
	want := append(rootRows(), row{
		ID: "u-foreign:0", Kind: usage.ItemAssistant, Text: "Line copied from another session.",
		Model: fixtureModel, Tokens: tokens(1, 1, 1, 1), Duration: noValue, Hook: noValue,
	})
	compareRows(t, rowsOf(page.Items), want)
}

func TestReadSubagentFixture(t *testing.T) {
	const webFetch = "WebFetch"
	page := mustRead(t, readFixture(t, fixtureSubFile), usage.TimelineRequest{SessionID: fixtureSession, AgentID: fixtureAgent})
	want := []row{
		{ID: "s-task:0", Kind: usage.ItemPrompt, Text: "List files importing config.", Tokens: noValue, Duration: noValue, Hook: noValue},
		{ID: "s-a1:0", Kind: usage.ItemToolCall, Model: fixtureModel, Tokens: tokens(10, 80, 9000, 500), Tool: webFetch, Category: usage.ToolBuiltin, UseID: "toolu_a1", Duration: noValue, Hook: noValue},
		{ID: "s-hook-1:0", Kind: usage.ItemHook, Tokens: noValue, Duration: noValue, Hook: "PreToolUse|PreToolUse:WebFetch|success|0|20"},
		{ID: "s-r1:0", Kind: usage.ItemToolResult, Text: "Docs summary", Tokens: noValue, Tool: webFetch, Category: usage.ToolBuiltin, UseID: "toolu_a1", Duration: "850", Hook: noValue},
		{ID: "s-a2:0", Kind: usage.ItemAssistant, Text: "3 files import config.", Model: fixtureModel, Tokens: tokens(4, 120, 9500, 20), Duration: noValue, Hook: noValue},
	}
	for i := range want {
		want[i].AgentID = fixtureAgent
	}
	compareRows(t, rowsOf(page.Items), want)
	if page.Skipped != 0 || page.NextCursor != "" {
		t.Errorf("Skipped = %d, NextCursor = %q; want 0 and empty", page.Skipped, page.NextCursor)
	}
}

func TestReadPagingMatchesFullRead(t *testing.T) {
	for _, name := range []string{fixtureRootFile, fixtureSubFile} {
		data := readFixture(t, name)
		full, _, fullSkipped := readAll(t, data, usage.TimelineRequest{SessionID: fixtureSession, Limit: usage.MaxPageSize})
		for limit := 1; limit <= len(full)+1; limit++ {
			t.Run(fmt.Sprintf("%s/limit=%d", name, limit), func(t *testing.T) {
				paged, pages, skipped := readAll(t, data, usage.TimelineRequest{SessionID: fixtureSession, Limit: limit})
				if !reflect.DeepEqual(paged, full) {
					t.Errorf("items of %d pages differ from the full read:\n got %+v\nwant %+v", pages, rowsOf(paged), rowsOf(full))
				}
				if skipped != fullSkipped {
					t.Errorf("Skipped over %d pages = %d; want %d", pages, skipped, fullSkipped)
				}
				if wantPages := (len(full) + limit - 1) / limit; pages != wantPages {
					t.Errorf("pages = %d; want %d", pages, wantPages)
				}
			})
		}
	}
}

// With one item per page every tool result is on a later page than its call
// and every model call is split across pages.
func TestReadResultOnLaterPage(t *testing.T) {
	data := readFixture(t, fixtureRootFile)
	req := usage.TimelineRequest{SessionID: fixtureSession, Limit: 1}
	var got []row
	for range maxTestPages {
		page := mustRead(t, data, req)
		if len(page.Items) != 1 {
			t.Fatalf("page holds %d items; want 1", len(page.Items))
		}
		got = append(got, rowOf(page.Items[0]))
		if page.NextCursor == "" {
			break
		}
		req.Cursor = page.NextCursor
	}
	compareRows(t, got, rootRows())
}

func TestReadKeepsLineItemsTogether(t *testing.T) {
	const at = "2026-03-01T09:00:00.000Z"
	twoResults := resultLine("r1", "2026-03-01T09:00:03.000Z", toolResult("t1", "alpha"), toolResult("t2", "beta"))
	twoResults["toolUseResult"] = obj{"durationMs": 77}
	data := transcript(t,
		promptLine("p1", at, "run both"),
		assistantLine("a1", at, "msg_1",
			obj{"type": "text", "text": "Running two tools."},
			toolUse("t1", "Read", obj{"file_path": "a.txt"}),
			toolUse("t2", "Read", obj{"file_path": "b.txt"}),
		),
		twoResults,
		promptLine("p2", at, "thanks"),
	)
	wantPerPage := [][]string{{"p1:0"}, {"a1:0", "a1:1", "a1:2"}, {"r1:0", "r1:1"}, {"p2:0"}}
	req := usage.TimelineRequest{Limit: 1}
	for i, want := range wantPerPage {
		page := mustRead(t, data, req)
		ids := make([]string, 0, len(page.Items))
		for _, it := range page.Items {
			ids = append(ids, it.ID)
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatalf("page %d: items %v; want %v", i, ids, want)
		}
		if last := i == len(wantPerPage)-1; (page.NextCursor == "") != last {
			t.Fatalf("page %d: NextCursor = %q; want empty only on the last page", i, page.NextCursor)
		}
		req.Cursor = page.NextCursor
	}

	// A page that is not full yet takes the whole next line, even when that
	// puts it over the limit.
	over := mustRead(t, data, usage.TimelineRequest{Limit: 2})
	if got := rowsOf(over.Items); len(got) != 4 || got[0].ID != "p1:0" || got[3].ID != "a1:2" {
		t.Errorf("limit 2: first page = %+v; want p1 and the three items of a1", got)
	}
	rest := mustRead(t, data, usage.TimelineRequest{Limit: 2, Cursor: over.NextCursor})
	if got := rowsOf(rest.Items); len(got) != 2 || got[0].Tool != "Read" || got[1].Tool != "Read" {
		t.Errorf("limit 2: second page = %+v; want the two results with their tool name", got)
	}

	// Only the first block of the call carries its usage, and two results on
	// one line cannot share the line's reported duration.
	items, _, _ := readAll(t, data, usage.TimelineRequest{})
	rows := rowsOf(items)
	if rows[1].Tokens != tokens(1, 2, 3, 4) || rows[2].Tokens != noValue || rows[3].Tokens != noValue {
		t.Errorf("tokens of the call's items = %q, %q, %q; want them on the first only", rows[1].Tokens, rows[2].Tokens, rows[3].Tokens)
	}
	if rows[4].Duration != "3000" || rows[5].Duration != "3000" {
		t.Errorf("durations = %q, %q; want 3000 for both", rows[4].Duration, rows[5].Duration)
	}
}

func TestReadLimit(t *testing.T) {
	const prompts = usage.MaxPageSize + 50
	lines := make([]any, 0, prompts)
	for i := range prompts {
		lines = append(lines, promptLine(fmt.Sprintf("p%d", i), "2026-03-01T09:00:00Z", "hello"))
	}
	data := transcript(t, lines...)
	for _, tc := range []struct {
		name  string
		limit int
		want  int
	}{
		{"zero uses the default", 0, usage.DefaultPageSize},
		{"small limit", 3, 3},
		{"at the cap", usage.MaxPageSize, usage.MaxPageSize},
		{"above the cap", usage.MaxPageSize * 10, usage.MaxPageSize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := mustRead(t, data, usage.TimelineRequest{Limit: tc.limit})
			if len(page.Items) != tc.want {
				t.Errorf("items = %d; want %d", len(page.Items), tc.want)
			}
			if page.NextCursor == "" {
				t.Error("NextCursor is empty; want a cursor for the remaining prompts")
			}
		})
	}
}

func TestReadEmptyAndItemless(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    []byte
		skipped int
	}{
		{"empty file", nil, 0},
		{"blank lines", []byte("\n\n  \n"), 0},
		{"lines the timeline does not show", transcript(t,
			obj{"type": "mode", "mode": "default"},
			obj{"type": "user", "uuid": "m1", "isMeta": true, "message": obj{"content": "generated"}},
			obj{"type": "system", "uuid": "s1", "subtype": "stop_hook_summary"},
			obj{"type": "attachment", "uuid": "x1", "attachment": obj{"type": "file", "exitCode": "n/a"}},
			obj{"type": "api-request-blob", "message": "not an object", "timestamp": 7},
			"null",
		), 0},
		{"only broken lines", []byte("{\"type\":\n[1,2]\n\"text\"\n"), 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := mustRead(t, tc.data, usage.TimelineRequest{})
			if page.Source != usage.SourceAvailable {
				t.Errorf("Source = %q; want %q", page.Source, usage.SourceAvailable)
			}
			if page.Items == nil || len(page.Items) != 0 {
				t.Errorf("Items = %#v; want an empty, non-nil slice", page.Items)
			}
			if page.NextCursor != "" || page.Skipped != tc.skipped {
				t.Errorf("NextCursor = %q, Skipped = %d; want empty and %d", page.NextCursor, page.Skipped, tc.skipped)
			}
		})
	}
}

func TestReadSkipsUnreadableLines(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	for _, tc := range []struct {
		name    string
		broken  any
		skipped int
	}{
		{"invalid JSON", `{"type":"user","message":`, 1},
		{"not an object", `["user"]`, 1},
		{"assistant message is a string", obj{"type": "assistant", "uuid": "b", "message": "oops"}, 1},
		{"content block is not an object", obj{"type": "user", "uuid": "b", "message": obj{"content": []any{"text"}}}, 1},
		{"usage is not numeric", obj{"type": "assistant", "uuid": "b", "message": obj{"usage": obj{"input_tokens": "many"}}}, 1},
		{"hook exit code is not numeric", obj{"type": "attachment", "uuid": "b", "attachment": obj{"type": "hook_success", "exitCode": "zero"}}, 1},
		{"unknown type with odd fields", obj{"type": "future-kind", "message": 3, "attachment": "x", "uuid": 9}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := transcript(t, promptLine("p1", at, "before"), tc.broken, promptLine("p2", at, "after"))
			items, _, skipped := readAll(t, data, usage.TimelineRequest{Limit: 1})
			if len(items) != 2 || items[0].Text != "before" || items[1].Text != "after" {
				t.Errorf("items = %+v; want the two prompts around the broken line", rowsOf(items))
			}
			if skipped != tc.skipped {
				t.Errorf("Skipped = %d; want %d", skipped, tc.skipped)
			}
		})
	}
}

func TestReadOversizedLine(t *testing.T) {
	const (
		at      = "2026-03-01T09:00:00Z"
		maxLine = 512
	)
	huge := promptLine("huge", at, strings.Repeat("x", maxLine))
	data := transcript(t, promptLine("p1", at, "before"), huge, promptLine("p2", at, "after"))

	for _, limit := range []int{1, usage.DefaultPageSize} {
		req := usage.TimelineRequest{Limit: limit}
		var ids []string
		skipped := 0
		for range maxTestPages {
			page, err := read(bytes.NewReader(data), req, maxLine)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			for _, it := range page.Items {
				ids = append(ids, it.ID)
			}
			skipped += page.Skipped
			if page.NextCursor == "" {
				break
			}
			req.Cursor = page.NextCursor
		}
		if want := []string{"p1:0", "p2:0"}; !reflect.DeepEqual(ids, want) || skipped != 1 {
			t.Errorf("limit %d: items %v, Skipped %d; want %v and 1", limit, ids, skipped, want)
		}
	}

	// A line of exactly the limit is still read.
	exact := bytes.Repeat([]byte(" "), maxLine)
	copy(exact, `{"type":"user","uuid":"fit","message":{"content":"fits"}}`)
	page, err := read(bytes.NewReader(append(exact, '\n')), usage.TimelineRequest{}, maxLine)
	if err != nil || len(page.Items) != 1 || page.Skipped != 0 {
		t.Errorf("line of the limit: items %d, Skipped %d, err %v; want 1, 0, nil", len(page.Items), page.Skipped, err)
	}
}

// The real limit is exercised once: a line just over usage.MaxLineBytes is
// passed over and the lines around it are still read.
func TestReadOversizedLineAtContractLimit(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	var data bytes.Buffer
	data.Write(transcript(t, promptLine("p1", at, "before")))
	data.WriteString(`{"type":"user","uuid":"huge","message":{"content":"`)
	data.Write(bytes.Repeat([]byte("x"), usage.MaxLineBytes))
	data.WriteString("\"}}\n")
	data.Write(transcript(t, promptLine("p2", at, "after")))

	page := mustRead(t, data.Bytes(), usage.TimelineRequest{})
	if len(page.Items) != 2 || page.Items[0].ID != "p1:0" || page.Items[1].ID != "p2:0" || page.Skipped != 1 {
		t.Errorf("items %+v, Skipped %d; want p1 and p2 and 1", rowsOf(page.Items), page.Skipped)
	}
}

func TestReadUnterminatedLastLine(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	complete := transcript(t, promptLine("p1", at, "first"), promptLine("p2", at, "second"))
	for _, tc := range []struct {
		name  string
		data  []byte
		items int
	}{
		{"complete JSON without newline", bytes.TrimSuffix(complete, []byte("\n")), 2},
		{"line still being written", append(bytes.Clone(complete), `{"type":"user","uuid":"p3","mess`...), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, _, skipped := readAll(t, tc.data, usage.TimelineRequest{Limit: 1})
			if len(items) != tc.items || skipped != 0 {
				t.Errorf("items = %d, Skipped = %d; want %d and 0", len(items), skipped, tc.items)
			}
		})
	}
}

func TestReadBadRequest(t *testing.T) {
	data := readFixture(t, fixtureRootFile)
	firstLineEnd := int64(bytes.IndexByte(data, '\n') + 1)
	cursorAt := func(offset int64) string {
		t.Helper()
		cursor, err := encodeCursor(offset, state{})
		if err != nil {
			t.Fatalf("encodeCursor: %v", err)
		}
		return cursor
	}
	wire := func(json string) string { return encodeRaw([]byte(json)) }
	manyCalls := state{}
	for i := range maxOpenCalls + 1 {
		manyCalls.open = append(manyCalls.open, openCall{ID: fmt.Sprintf("t%d", i), Name: "Read", Category: usage.ToolBuiltin})
	}
	tooMany, err := encodeCursor(0, manyCalls)
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	longID := strings.Repeat("i", maxIDLen+1)

	for _, tc := range []struct {
		name string
		req  usage.TimelineRequest
	}{
		{"negative limit", usage.TimelineRequest{Limit: -1}},
		{"not base64", usage.TimelineRequest{Cursor: "***"}},
		{"padded base64", usage.TimelineRequest{Cursor: cursorAt(0) + "="}},
		{"not JSON", usage.TimelineRequest{Cursor: encodeRaw([]byte("offset 12"))}},
		{"wrong JSON type", usage.TimelineRequest{Cursor: wire(`[1,0]`)}},
		{"unknown field", usage.TimelineRequest{Cursor: wire(`{"v":1,"o":0,"x":1}`)}},
		{"trailing data", usage.TimelineRequest{Cursor: wire(`{"v":1,"o":0}{}`)}},
		{"missing version", usage.TimelineRequest{Cursor: wire(`{"o":0}`)}},
		{"future version", usage.TimelineRequest{Cursor: wire(`{"v":2,"o":0}`)}},
		{"negative offset", usage.TimelineRequest{Cursor: wire(`{"v":1,"o":-1}`)}},
		{"offset inside a line", usage.TimelineRequest{Cursor: cursorAt(firstLineEnd - 1)}},
		{"offset inside the first line", usage.TimelineRequest{Cursor: cursorAt(1)}},
		{"offset beyond the file", usage.TimelineRequest{Cursor: cursorAt(int64(len(data)) + 1)}},
		{"offset far beyond the file", usage.TimelineRequest{Cursor: wire(`{"v":1,"o":9223372036854775807}`)}},
		{"too many open calls", usage.TimelineRequest{Cursor: tooMany}},
		{"open call without id", usage.TimelineRequest{Cursor: wire(`{"v":1,"o":0,"c":[{"i":"","n":"Read","k":"builtin","t":0}]}`)}},
		{"open call with long id", usage.TimelineRequest{Cursor: wire(`{"v":1,"o":0,"c":[{"i":"` + longID + `","n":"Read","k":"builtin","t":0}]}`)}},
		{"open call with long name", usage.TimelineRequest{Cursor: wire(`{"v":1,"o":0,"c":[{"i":"t","n":"` + strings.Repeat("n", usage.MaxLabelLen+1) + `","k":"builtin","t":0}]}`)}},
		{"open call with unknown category", usage.TimelineRequest{Cursor: wire(`{"v":1,"o":0,"c":[{"i":"t","n":"Read","k":"other","t":0}]}`)}},
		{"open call with negative time", usage.TimelineRequest{Cursor: wire(`{"v":1,"o":0,"c":[{"i":"t","n":"Read","k":"builtin","t":-5}]}`)}},
		{"long call id", usage.TimelineRequest{Cursor: wire(`{"v":1,"o":0,"m":{"i":"` + longID + `"}}`)}},
		{"oversized cursor", usage.TimelineRequest{Cursor: strings.Repeat("A", maxCursorLen+1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := Read(bytes.NewReader(data), tc.req)
			if !errors.Is(err, usage.ErrBadRequest) {
				t.Fatalf("err = %v; want usage.ErrBadRequest", err)
			}
			if len(page.Items) != 0 || page.NextCursor != "" {
				t.Errorf("page = %+v; want an empty page with the error", page)
			}
		})
	}

	if _, err := Read(nil, usage.TimelineRequest{}); !errors.Is(err, usage.ErrBadRequest) {
		t.Errorf("nil reader: err = %v; want usage.ErrBadRequest", err)
	}
}

func TestReadCursorAtLineStartIsAccepted(t *testing.T) {
	data := readFixture(t, fixtureRootFile)
	end, err := encodeCursor(int64(len(data)), state{})
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	page := mustRead(t, data, usage.TimelineRequest{Cursor: end})
	if len(page.Items) != 0 || page.NextCursor != "" || page.Skipped != 0 {
		t.Errorf("page at the end of the file = %+v; want empty", page)
	}

	afterFirst, err := encodeCursor(int64(bytes.IndexByte(data, '\n')+1), state{})
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	page = mustRead(t, data, usage.TimelineRequest{SessionID: fixtureSession, Cursor: afterFirst, Limit: 1})
	if len(page.Items) != 1 || page.Items[0].ID != "u-hook-1:0" {
		t.Errorf("page after the first line = %+v; want the first hook", rowsOf(page.Items))
	}
}

// failingReader stands in for a transcript that stops being readable.
type failingReader struct {
	seekErr error
	readErr error
}

func (f failingReader) Seek(int64, int) (int64, error) { return 0, f.seekErr }
func (f failingReader) Read([]byte) (int, error)       { return 0, f.readErr }

func TestReadReportsIOErrors(t *testing.T) {
	errDisk := errors.New("synthetic disk failure")
	inLine, err := encodeCursor(10, state{})
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}
	for _, tc := range []struct {
		name   string
		reader io.ReadSeeker
		cursor string
	}{
		{"seek fails", failingReader{seekErr: errDisk}, ""},
		{"seek to a cursor fails", failingReader{seekErr: errDisk}, inLine},
		{"read fails", failingReader{readErr: errDisk}, ""},
		{"read before a cursor fails", failingReader{readErr: errDisk}, inLine},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := Read(tc.reader, usage.TimelineRequest{Cursor: tc.cursor})
			if !errors.Is(err, errDisk) {
				t.Fatalf("err = %v; want it to wrap the reader's error", err)
			}
			if errors.Is(err, usage.ErrBadRequest) {
				t.Errorf("err = %v; an I/O failure is not a bad request", err)
			}
			if page.Source != "" || page.Items != nil {
				t.Errorf("page = %+v; want the zero page with an error", page)
			}
		})
	}
}

func TestReadOpenCallsAreCapped(t *testing.T) {
	const (
		at    = "2026-03-01T09:00:00.000Z"
		later = "2026-03-01T09:00:05.000Z"
		// More calls than two full pages, so that the second cursor is taken
		// after the cap was reached.
		readCalls = 2 * usage.MaxPageSize
		calls     = readCalls + 6
		evicted   = readCalls - maxOpenCalls
	)
	lines := make([]any, 0, calls+3)
	for i := range calls {
		lines = append(lines, assistantLine(fmt.Sprintf("a%d", i), at, fmt.Sprintf("msg_%d", i), toolUse(fmt.Sprintf("t%d", i), "Read", obj{})))
	}
	lines = append(lines,
		resultLine("r-old", later, toolResult("t0", "evicted")),
		resultLine("r-new", later, toolResult(fmt.Sprintf("t%d", calls-1), "kept")),
		resultLine("r-none", later, toolResult("never-called", "orphan")),
	)
	data := transcript(t, lines...)

	full, _, _ := readAll(t, data, usage.TimelineRequest{Limit: usage.MaxPageSize})
	paged, _, _ := readAll(t, data, usage.TimelineRequest{Limit: 7})
	if !reflect.DeepEqual(paged, full) {
		t.Fatal("paged read differs from the full read once calls are evicted")
	}
	results := rowsOf(full[calls:])
	if results[0].Tool != "" || results[0].Category != "" || results[0].Duration != noValue {
		t.Errorf("result of an evicted call = %+v; want no name, category or duration", results[0])
	}
	if results[1].Tool != "Read" || results[1].Category != usage.ToolBuiltin || results[1].Duration != "5000" {
		t.Errorf("result of a kept call = %+v; want Read, builtin, 5000", results[1])
	}
	if results[2].Tool != "" || results[2].UseID != "never-called" || results[2].Duration != noValue {
		t.Errorf("result without a call = %+v; want only the use ID", results[2])
	}

	// The cursor never carries more than the cap, however many calls stay open.
	first := mustRead(t, data, usage.TimelineRequest{Limit: usage.MaxPageSize})
	second := mustRead(t, data, usage.TimelineRequest{Limit: usage.MaxPageSize, Cursor: first.NextCursor})
	_, st, err := decodeCursor(second.NextCursor)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if len(st.open) != maxOpenCalls || st.open[0].ID != fmt.Sprintf("t%d", evicted) {
		t.Errorf("cursor holds %d open calls starting at %q; want %d starting at t%d", len(st.open), st.open[0].ID, maxOpenCalls, evicted)
	}
	if len(second.NextCursor) > maxCursorLen {
		t.Errorf("cursor is %d bytes; the reader would refuse its own cursor above %d", len(second.NextCursor), maxCursorLen)
	}
}

// A call time before the Unix epoch is treated as unknown: the cursor stays
// valid and the result still names its call, without a duration.
func TestReadCallTimeBeforeEpoch(t *testing.T) {
	data := transcript(t,
		assistantLine("a1", "1960-01-01T00:00:00Z", "msg_1", toolUse("t1", "Read", obj{})),
		resultLine("r1", "2026-03-01T09:00:03Z", toolResult("t1", "late")),
	)
	items, pages, _ := readAll(t, data, usage.TimelineRequest{Limit: 1})
	if pages != 2 || len(items) != 2 {
		t.Fatalf("pages = %d, items = %d; want 2 and 2", pages, len(items))
	}
	if got := rowOf(items[1]); got.Tool != "Read" || got.Duration != noValue {
		t.Errorf("result = %+v; want the tool name without a duration", got)
	}
}

// A tool-use ID that shows up twice is tracked once, with its latest call.
func TestReadRepeatedToolUseID(t *testing.T) {
	data := transcript(t,
		assistantLine("a1", "2026-03-01T09:00:00.000Z", "msg_1", toolUse("t1", "Read", obj{})),
		assistantLine("a2", "2026-03-01T09:00:02.000Z", "msg_2", toolUse("t1", "Grep", obj{})),
		resultLine("r1", "2026-03-01T09:00:03.000Z", toolResult("t1", "first")),
		resultLine("r2", "2026-03-01T09:00:04.000Z", toolResult("t1", "second")),
	)
	for _, limit := range []int{1, usage.DefaultPageSize} {
		items, _, _ := readAll(t, data, usage.TimelineRequest{Limit: limit})
		rows := rowsOf(items)
		if rows[2].Tool != "Grep" || rows[2].Duration != "1000" {
			t.Errorf("limit %d: first result = %+v; want the latest call, Grep after 1000 ms", limit, rows[2])
		}
		if rows[3].Tool != "" || rows[3].Duration != noValue {
			t.Errorf("limit %d: second result = %+v; want no call left to name", limit, rows[3])
		}
	}
}

func TestReadCursorDropsClosedCalls(t *testing.T) {
	data := readFixture(t, fixtureRootFile)
	req := usage.TimelineRequest{SessionID: fixtureSession, Limit: 1}
	wantOpen := map[string]int{"u-a1-2:0": 1, "u-hook-2:0": 1, "u-r1:0": 0, "u-a2:0": 1, "u-r2:0": 0}
	for range maxTestPages {
		page := mustRead(t, data, req)
		if page.NextCursor == "" {
			break
		}
		_, st, err := decodeCursor(page.NextCursor)
		if err != nil {
			t.Fatalf("decodeCursor: %v", err)
		}
		id := page.Items[0].ID
		if want, ok := wantOpen[id]; ok && len(st.open) != want {
			t.Errorf("after %s the cursor holds %d open calls; want %d", id, len(st.open), want)
		}
		req.Cursor = page.NextCursor
	}
}

func TestReadItemIDs(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	first := obj{"type": "user", "timestamp": at, "message": obj{"content": "no uuid"}}
	data := transcript(t,
		first,
		obj{"type": "user", "uuid": strings.Repeat("u", maxIDLen+1), "timestamp": at, "message": obj{"content": "long uuid"}},
		obj{"type": "user", "uuid": 12, "timestamp": at, "message": obj{"content": "numeric uuid"}},
		promptLine("p1", at, "plain"),
		resultLine("r1", at, obj{"type": "text", "text": "note"}, obj{"type": "text", "text": "more"}),
		// A uuid shaped like an offset ID must not take the ID of line one.
		promptLine("@0", at, "impostor"),
	)
	firstLen := len(transcript(t, first))
	items, _, _ := readAll(t, data, usage.TimelineRequest{})
	if len(items) != 6 {
		t.Fatalf("items = %d; want 6", len(items))
	}
	if items[5].ID == items[0].ID || !strings.HasPrefix(items[5].ID, "@") {
		t.Errorf("ID of the line with an offset-shaped uuid = %q; want its own offset ID", items[5].ID)
	}
	if items[0].ID != "@0:0" || items[1].ID != fmt.Sprintf("@%d:0", firstLen) || items[3].ID != "p1:0" {
		t.Errorf("IDs = %q, %q, %q; want @0:0, @%d:0, p1:0", items[0].ID, items[1].ID, items[3].ID, firstLen)
	}
	if items[4].Kind != usage.ItemPrompt || items[4].Text != "note\nmore" {
		t.Errorf("text blocks = %+v; want one prompt joining both", rowOf(items[4]))
	}
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.ID] {
			t.Errorf("ID %q is used twice", it.ID)
		}
		seen[it.ID] = true
	}

	root, _, _ := readAll(t, readFixture(t, fixtureRootFile), usage.TimelineRequest{})
	seen = map[string]bool{}
	for _, it := range root {
		if seen[it.ID] {
			t.Errorf("fixture ID %q is used twice", it.ID)
		}
		seen[it.ID] = true
	}
}

// The reader does not depend on how the writer spaces its JSON, and takes a
// message whose content is a bare string on either side of the conversation.
func TestReadLooseFormatting(t *testing.T) {
	data := []byte(strings.Join([]string{
		`  { "type" : "user" , "uuid" : "p1" , "timestamp" : "2026-03-01T09:00:00Z" , "message" : { "content" : [ { "type" : "text" , "text" : "spaced" } ] } }  `,
		"{\"type\":\"assistant\",\"uuid\":\"a1\",\"message\":{\"id\":\"msg_1\",\"model\":\"" + fixtureModel + "\",\"content\":\"bare reply\"}}\r",
		"\t{ \"type\" : \"attachment\" , \"uuid\" : \"h1\" , \"attachment\" : { \"type\" : \"hook_success\" , \"hookName\" : \"Stop\" } }",
		`{ "type" : "system" , "uuid" : "s1" , "subtype" : "compact_boundary" , "compactMetadata" : { "trigger" : "manual" } }`,
		"",
	}, "\n"))
	items, _, skipped := readAll(t, data, usage.TimelineRequest{Limit: 1})
	compareRows(t, rowsOf(items), []row{
		{ID: "p1:0", Kind: usage.ItemPrompt, Text: "spaced", Tokens: noValue, Duration: noValue, Hook: noValue},
		{ID: "a1:0", Kind: usage.ItemAssistant, Text: "bare reply", Model: fixtureModel, Tokens: noValue, Duration: noValue, Hook: noValue},
		{ID: "h1:0", Kind: usage.ItemHook, Tokens: noValue, Duration: noValue, Hook: "|Stop|success|0|0"},
		{
			ID: "s1:0", Kind: usage.ItemSystem, Label: "Context compacted (manual)", Tokens: noValue, Duration: noValue, Hook: noValue,
			System: systemRow(usage.SystemCompaction, noValue, "manual"),
		},
	})
	if skipped != 0 {
		t.Errorf("Skipped = %d; want 0", skipped)
	}
}

func TestReadSystemLabels(t *testing.T) {
	system := func(fields obj) obj {
		fields["type"], fields["uuid"] = "system", "s1"
		return fields
	}
	turnEnd := func(ms string) string { return systemRow(usage.SystemTurnEnd, ms, "") }
	compaction := func(trigger string) string { return systemRow(usage.SystemCompaction, noValue, trigger) }
	for _, tc := range []struct {
		name       string
		line       obj
		want       string
		wantSystem string
	}{
		{"turn duration", system(obj{"subtype": "turn_duration", "durationMs": 54040}), "Turn ended · 54.0 s", turnEnd("54040")},
		{"turn duration rounds to one decimal", system(obj{"subtype": "turn_duration", "durationMs": 1250.4}), "Turn ended · 1.3 s", turnEnd("1250")},
		{"turn duration missing", system(obj{"subtype": "turn_duration"}), "Turn ended", turnEnd(noValue)},
		{"turn duration is text", system(obj{"subtype": "turn_duration", "durationMs": "long"}), "Turn ended", turnEnd(noValue)},
		{"negative turn duration", system(obj{"subtype": "turn_duration", "durationMs": -5}), "Turn ended · 0.0 s", turnEnd("0")},
		{"manual compaction", system(obj{"subtype": "compact_boundary", "compactMetadata": obj{"trigger": "manual"}}), "Context compacted (manual)", compaction("manual")},
		{"compaction without trigger", system(obj{"subtype": "compact_boundary"}), "Context compacted", compaction("")},
		{"compaction with odd metadata", system(obj{"subtype": "compact_boundary", "compactMetadata": "x"}), "Context compacted", compaction("")},
		{"other subtype", system(obj{"subtype": "informational", "content": "note"}), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := mustRead(t, transcript(t, tc.line), usage.TimelineRequest{})
			if tc.want == "" {
				if len(page.Items) != 0 {
					t.Fatalf("items = %+v; want none", rowsOf(page.Items))
				}
				return
			}
			if len(page.Items) != 1 || page.Items[0].Kind != usage.ItemSystem || page.Items[0].Label != tc.want {
				t.Fatalf("items = %+v; want one system item labelled %q", rowsOf(page.Items), tc.want)
			}
			if got := rowOf(page.Items[0]).System; got != tc.wantSystem {
				t.Errorf("system = %q; want %q", got, tc.wantSystem)
			}
		})
	}
}

func TestHookStatus(t *testing.T) {
	for attachmentType, want := range map[string]usage.HookStatus{
		"hook_success":            usage.HookSuccess,
		"hook_non_blocking_error": usage.HookError,
		"hook_cancelled":          usage.HookError,
		"hook_blocking_error":     usage.HookBlocked,
		"hook_additional_context": usage.HookUnknown,
		"hook_":                   usage.HookUnknown,
	} {
		if got := hookStatus(attachmentType); got != want {
			t.Errorf("hookStatus(%q) = %q; want %q", attachmentType, got, want)
		}
	}
}

func TestCategoryOf(t *testing.T) {
	for name, want := range map[string]usage.ToolCategory{
		"Bash":                             usage.ToolBuiltin,
		"Read":                             usage.ToolBuiltin,
		"":                                 usage.ToolBuiltin,
		"Skill":                            usage.ToolSkill,
		"Agent":                            usage.ToolAgent,
		"Task":                             usage.ToolAgent,
		"TaskOutput":                       usage.ToolBuiltin,
		"mcp__issues__search":              usage.ToolMCP,
		"mcp__plugin_devkit_chain__status": usage.ToolMCP,
		"mcp_single_underscore":            usage.ToolBuiltin,
	} {
		if got := categoryOf(name); got != want {
			t.Errorf("categoryOf(%q) = %q; want %q", name, got, want)
		}
		if !knownCategory(categoryOf(name)) {
			t.Errorf("categoryOf(%q) is not a known category", name)
		}
	}
}

func TestReadModelCallEdges(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	usageWith := func(details any) obj {
		return obj{"input_tokens": 1, "output_tokens": 2, "cache_read_input_tokens": 3, "cache_creation_input_tokens": 4, "output_tokens_details": details}
	}
	thinking := obj{"type": "thinking", "thinking": "private reasoning", "signature": "sig"}
	message := func(uuid, id string, u any, blocks ...obj) obj {
		body := obj{"id": id, "model": fixtureModel, "content": blocks}
		if u != nil {
			body["usage"] = u
		}
		return obj{"type": "assistant", "uuid": uuid, "timestamp": at, "message": body}
	}
	data := transcript(t,
		// Two thinking blocks of one call: the thinking tokens are reported once.
		message("a1", "msg_1", usageWith(obj{"thinking_tokens": 70}), thinking, thinking, obj{"type": "text", "text": "done"}),
		// Thinking tokens not reported at all: unknown, not zero.
		message("a2", "msg_2", usageWith(nil), thinking),
		// No usage on the line: the model is still named.
		message("a3", "msg_3", nil, obj{"type": "text", "text": "no usage"}),
		// No message ID: every line is a call of its own.
		message("a4", "", usageWith(nil), obj{"type": "text", "text": "first"}),
		message("a5", "", usageWith(nil), obj{"type": "text", "text": "second"}),
		// Empty text and unknown block types add nothing.
		message("a6", "msg_6", usageWith(nil), obj{"type": "text", "text": ""}, obj{"type": "server_tool_use", "id": "s1", "name": "search"}, obj{"type": "redacted_thinking", "data": "opaque"}),
	)
	items, _, _ := readAll(t, data, usage.TimelineRequest{Limit: 2})
	bare := func(id string, kind usage.TimelineKind, text, model, tok string) row {
		return row{ID: id, Kind: kind, Text: text, Model: model, Tokens: tok, Duration: noValue, Hook: noValue}
	}
	call := tokens(1, 2, 3, 4)
	compareRows(t, rowsOf(items), []row{
		bare("a1:0", usage.ItemThinking, "", fixtureModel, tokens(0, 70, 0, 0)),
		bare("a1:1", usage.ItemThinking, "", fixtureModel, noValue),
		bare("a1:2", usage.ItemAssistant, "done", fixtureModel, call),
		bare("a2:0", usage.ItemThinking, "", fixtureModel, noValue),
		bare("a3:0", usage.ItemAssistant, "no usage", fixtureModel, noValue),
		bare("a4:0", usage.ItemAssistant, "first", fixtureModel, call),
		bare("a5:0", usage.ItemAssistant, "second", fixtureModel, call),
		bare("a6:2", usage.ItemThinking, "", fixtureModel, noValue),
	})
	for _, it := range items {
		if strings.Contains(it.Text, "private") {
			t.Errorf("%s leaks thinking text", it.ID)
		}
	}
}

func TestReadToolResultContent(t *testing.T) {
	const at = "2026-03-01T09:00:00.000Z"
	result := func(content any, toolUseResult any) []byte {
		line := resultLine("r1", "2026-03-01T09:00:01.500Z", toolResult("t1", content))
		if toolUseResult != nil {
			line["toolUseResult"] = toolUseResult
		}
		return transcript(t, assistantLine("a1", at, "msg_1", toolUse("t1", "Read", obj{})), line)
	}
	for _, tc := range []struct {
		name     string
		data     []byte
		text     string
		duration string
	}{
		{"string content", result("plain output", nil), "plain output", "1500"},
		{"text blocks are joined", result([]obj{{"type": "text", "text": "one"}, {"type": "image", "source": obj{"data": "AAAA"}}, {"type": "text", "text": "two"}}, nil), "one\ntwo", "1500"},
		{"no text block", result([]obj{{"type": "tool_reference", "tool_name": "Read"}}, nil), "", "1500"},
		{"null content", result(nil, nil), "", "1500"},
		{"odd content", result([]any{"bare"}, nil), "", "1500"},
		{"reported duration wins", result("x", obj{"durationMs": 42}), "x", "42"},
		{"fractional reported duration", result("x", obj{"durationMs": 42.9}), "x", "42"},
		{"zero reported duration falls back", result("x", obj{"durationMs": 0}), "x", "1500"},
		{"textual reported duration falls back", result("x", obj{"durationMs": "42"}), "x", "1500"},
		{"toolUseResult is a string", result("x", "Error: failed"), "x", "1500"},
		{"toolUseResult is a list", result("x", []obj{{"type": "text"}}), "x", "1500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, _, _ := readAll(t, tc.data, usage.TimelineRequest{Limit: 1})
			if len(items) != 2 {
				t.Fatalf("items = %d; want the call and its result", len(items))
			}
			got := rowOf(items[1])
			if got.Kind != usage.ItemToolResult || got.Text != tc.text || got.Duration != tc.duration || got.Tool != "Read" {
				t.Errorf("result = %+v; want text %q, duration %s, tool Read", got, tc.text, tc.duration)
			}
		})
	}

	// A result that is earlier than its call, or without a time, has no
	// derived duration.
	for name, resultAt := range map[string]string{"result before call": "2026-03-01T08:59:59Z", "no result time": ""} {
		data := transcript(t,
			assistantLine("a1", at, "msg_1", toolUse("t1", "Read", obj{})),
			resultLine("r1", resultAt, toolResult("t1", "x")),
		)
		items, _, _ := readAll(t, data, usage.TimelineRequest{})
		if got := rowOf(items[1]); got.Duration != noValue || got.Tool != "Read" {
			t.Errorf("%s: result = %+v; want the tool name without a duration", name, got)
		}
	}
}
