package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Identifiers of the synthetic stream the edge-case tests build.
const (
	testSession = "22222222-2222-4222-8222-222222222222"
	testOther   = "33333333-3333-4333-8333-333333333333"
	testAgent   = "c0ffee"
	testTime    = "2026-04-02T10:00:00.000Z"
	testLater   = "2026-04-02T10:05:00.000Z"
)

// obj is one transcript line under construction. encoding/json writes the
// keys in sorted order, which puts "type" behind "message" and "attachment"
// as real transcripts do.
type obj map[string]any

// jsonl encodes the lines, each ending in a newline.
func jsonl(t *testing.T, lines ...obj) []byte {
	t.Helper()
	var out bytes.Buffer
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("encode test line: %v", err)
		}
		out.Write(encoded)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// with returns a copy of line with extra members set.
func with(line obj, extra obj) obj {
	out := obj{}
	for k, v := range line {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func base(kind, uuid, at string) obj {
	line := obj{"type": kind, "uuid": uuid, "sessionId": testSession}
	if at != "" {
		line["timestamp"] = at
	}
	return line
}

func promptLine(uuid, at, text string) obj {
	return with(base("user", uuid, at), obj{"promptSource": "typed", "message": obj{"role": "user", "content": text}})
}

func assistantLine(uuid, at, messageID string, blocks ...obj) obj {
	return with(base("assistant", uuid, at), obj{"message": obj{
		"id": messageID, "model": "model-small", "stop_reason": "end_turn", "content": blocks,
		"usage": obj{"input_tokens": 1, "output_tokens": 2, "cache_read_input_tokens": 3, "cache_creation_input_tokens": 4},
	}})
}

func toolUse(id, name string, input obj) obj {
	return obj{"type": "tool_use", "id": id, "name": name, "input": input}
}

func resultLine(uuid, at, toolUseID string) obj {
	return with(base("user", uuid, at), obj{"message": obj{"role": "user", "content": []obj{
		{"type": "tool_result", "tool_use_id": toolUseID, "content": "output"},
	}}})
}

func hookLine(uuid, at, kind string) obj {
	return with(base("attachment", uuid, at), obj{"attachment": obj{
		"type": kind, "hookEvent": "PreToolUse", "hookName": "PreToolUse:Bash", "toolUseID": "toolu_x",
		"exitCode": 0, "durationMs": 5, "stdout": "", "stderr": "",
	}})
}

func rootInput() usage.ScanInput {
	return usage.ScanInput{SessionID: testSession}
}

func TestScanEmptyInput(t *testing.T) {
	const start = 4096
	got := mustScan(t, nil, usage.ScanInput{SessionID: testSession, AgentID: testAgent, Base: start})
	want := usage.Batch{
		BaseOffset: start,
		NextOffset: start,
		Session:    usage.SessionPatch{SessionID: testSession, AgentID: testAgent},
	}
	diff(t, got, want)
}

func TestScanLeavesTrailingPartialLine(t *testing.T) {
	first := jsonl(t, promptLine("p1", testTime, "one"))
	second := jsonl(t, promptLine("p2", testLater, "two"))
	cut := len(second) / 2
	data := append(append([]byte{}, first...), second[:cut]...)

	got := mustScan(t, data, rootInput())
	if got.NextOffset != int64(len(first)) || got.Lines != 1 || got.Skipped != 0 {
		t.Fatalf("offset %d, lines %d, skipped %d; want offset %d, one line, nothing skipped",
			got.NextOffset, got.Lines, got.Skipped, len(first))
	}
	if len(got.Prompts) != 1 || got.Prompts[0].Key != "p1" {
		t.Fatalf("prompts = %+v, want only p1", got.Prompts)
	}

	// The writer finishes the line; the next call starts where the first
	// stopped and reads it whole.
	data = append(data, second[cut:]...)
	rest := mustScan(t, data[got.NextOffset:], usage.ScanInput{SessionID: testSession, Base: got.NextOffset})
	if rest.NextOffset != int64(len(data)) || len(rest.Prompts) != 1 || rest.Prompts[0].Key != "p2" {
		t.Fatalf("resume: offset %d, prompts %+v; want offset %d and only p2", rest.NextOffset, rest.Prompts, len(data))
	}

	// A complete object without its newline is still a partial line.
	whole := bytes.TrimSuffix(first, []byte("\n"))
	if b := mustScan(t, whole, rootInput()); b.NextOffset != 0 || b.Lines != 0 || len(b.Prompts) != 0 {
		t.Fatalf("unterminated line was consumed: %+v", b)
	}
}

func TestScanMaxBytesStopsAtLineBoundary(t *testing.T) {
	lines := [][]byte{
		jsonl(t, promptLine("p1", testTime, "one")),
		jsonl(t, promptLine("p2", testTime, "two")),
		jsonl(t, promptLine("p3", testTime, "three")),
	}
	data := bytes.Join(lines, nil)
	one, two := int64(len(lines[0])), int64(len(lines[0])+len(lines[1]))
	cases := []struct {
		name     string
		maxBytes int64
		want     int64
	}{
		{"unlimited", 0, int64(len(data))},
		{"negative is unlimited", -1, int64(len(data))},
		{"inside the first line", 1, one},
		{"exactly the first line", one, one},
		{"one byte into the second line", one + 1, two},
		{"beyond the end", int64(len(data)) + 100, int64(len(data))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustScan(t, data, usage.ScanInput{SessionID: testSession, MaxBytes: tc.maxBytes})
			if got.NextOffset != tc.want {
				t.Fatalf("NextOffset = %d, want %d", got.NextOffset, tc.want)
			}
		})
	}

	// A second call continues with the remaining lines and repeats nothing.
	firstCall := mustScan(t, data, usage.ScanInput{SessionID: testSession, MaxBytes: 1})
	secondCall := mustScan(t, data[firstCall.NextOffset:], usage.ScanInput{SessionID: testSession, Base: firstCall.NextOffset})
	keys := make([]string, 0, len(lines))
	for _, call := range []usage.Batch{firstCall, secondCall} {
		for _, p := range call.Prompts {
			keys = append(keys, p.Key)
		}
	}
	if strings.Join(keys, ",") != "p1,p2,p3" || secondCall.NextOffset != int64(len(data)) {
		t.Fatalf("prompts over two calls = %v, end %d; want p1,p2,p3 and %d", keys, secondCall.NextOffset, len(data))
	}
}

// rawLine builds a line by hand where the member order matters.
func rawLine(prefix string, fill int, suffix string) string {
	return prefix + strings.Repeat("x", fill) + suffix + "\n"
}

func TestScanOversizedLines(t *testing.T) {
	lim := limits{line: 400, sniff: 160}
	const fill = 1000
	before := string(jsonl(t, promptLine("p1", testTime, "before")))
	after := string(jsonl(t, promptLine("p2", testLater, "after")))
	if len(before) > lim.line || len(after) > lim.line {
		t.Fatalf("test lines of %d and %d bytes do not fit the %d byte limit", len(before), len(after), lim.line)
	}
	cases := []struct {
		name          string
		line          string
		skip, foreign int
	}{
		{"read kind named in the prefix",
			rawLine(`{"type":"user","sessionId":"`+testSession+`","uuid":"big","message":{"role":"user","content":"`, fill, `"}}`), 1, 0},
		{"kind the scanner does not read",
			rawLine(`{"type":"file-history-snapshot","sessionId":"`+testSession+`","payload":"`, fill, `"}`), 0, 0},
		{"kind named after the prefix",
			rawLine(`{"message":{"role":"assistant","content":"`, fill, `"},"type":"assistant","sessionId":"`+testSession+`"}`), 0, 0},
		{"line of another session",
			rawLine(`{"type":"user","sessionId":"`+testOther+`","message":{"content":"`, fill, `"}}`), 0, 1},
		{"not json at all", rawLine("", fill, ""), 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := before + tc.line + after
			got, err := scan(strings.NewReader(data), rootInput(), lim)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got.Skipped != tc.skip || got.Foreign != tc.foreign || got.Lines != 3 {
				t.Errorf("skipped %d, foreign %d, lines %d; want %d, %d, 3", got.Skipped, got.Foreign, got.Lines, tc.skip, tc.foreign)
			}
			if got.NextOffset != int64(len(data)) {
				t.Errorf("NextOffset = %d, want %d", got.NextOffset, len(data))
			}
			// The lines around the oversized one are read as usual.
			if len(got.Prompts) != 2 || got.Prompts[0].Key != "p1" || got.Prompts[1].Key != "p2" {
				t.Errorf("prompts = %+v, want p1 and p2", got.Prompts)
			}
			if !got.Session.LastAt.Equal(instant(t, testLater)) {
				t.Errorf("LastAt = %v, want the time of the line after the oversized one", got.Session.LastAt)
			}
		})
	}
}

func TestScanLineLengthLimit(t *testing.T) {
	lim := limits{line: 400, sniff: 160}
	prefix := `{"type":"user","sessionId":"` + testSession + `","uuid":"edge","message":{"role":"user","content":"`
	const suffix = `"}}`
	room := lim.line - len(prefix) - len(suffix)
	cases := []struct {
		name          string
		fill          int
		prompts, skip int
	}{
		{"exactly at the limit", room, 1, 0},
		{"one byte over", room + 1, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scan(strings.NewReader(rawLine(prefix, tc.fill, suffix)), rootInput(), lim)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(got.Prompts) != tc.prompts || got.Skipped != tc.skip {
				t.Fatalf("prompts %d, skipped %d; want %d, %d", len(got.Prompts), got.Skipped, tc.prompts, tc.skip)
			}
			if tc.prompts == 1 && got.Prompts[0].Chars != tc.fill {
				t.Fatalf("Chars = %d, want %d", got.Prompts[0].Chars, tc.fill)
			}
		})
	}

	// An oversized line that the writer has not finished is not consumed.
	unfinished := strings.TrimSuffix(rawLine(prefix, 10*lim.line, suffix), "\n")
	got, err := scan(strings.NewReader(unfinished), rootInput(), lim)
	if err != nil || got.NextOffset != 0 || got.Lines != 0 || got.Skipped != 0 {
		t.Fatalf("unfinished oversized line: %+v, %v; want nothing consumed", got, err)
	}
}

// Lines longer than the read buffer are assembled from several reads.
func TestScanLineLongerThanReadBuffer(t *testing.T) {
	const fill = 3*readBufferBytes + 17
	data := jsonl(t, promptLine("long", testTime, strings.Repeat("y", fill)), promptLine("short", testLater, "z"))

	got := mustScan(t, data, rootInput())
	if len(got.Prompts) != 2 || got.Prompts[0].Chars != fill || got.Prompts[1].Chars != 1 {
		t.Fatalf("prompts = %+v, want %d and 1 characters", got.Prompts, fill)
	}

	lim := limits{line: readBufferBytes + 1, sniff: readBufferBytes / 2}
	skipped, err := scan(bytes.NewReader(data), rootInput(), lim)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if skipped.Skipped != 0 || len(skipped.Prompts) != 1 || skipped.NextOffset != int64(len(data)) {
		// encoding/json puts "message" before "type", so the prefix of the
		// long line does not name its kind and the line is not counted.
		t.Fatalf("with a small limit: skipped %d, prompts %+v, offset %d", skipped.Skipped, skipped.Prompts, skipped.NextOffset)
	}
}

func TestScanMalformedAndUnknownLines(t *testing.T) {
	sid := `"sessionId":"` + testSession + `"`
	cases := []struct {
		name string
		line string
		skip int
	}{
		{"blank", "", 0},
		{"whitespace", " \t ", 0},
		{"text", "not json", 1},
		{"array", `[1,2,3]`, 1},
		{"scalar", `42`, 1},
		{"truncated before the type", `{"parentUuid":null,"mess`, 1},
		{"truncated line of a read kind", `{"type":"assistant",` + sid + `,"message":`, 1},
		{"truncated line of another kind", `{"type":"mode",` + sid + `,"mode":`, 0},
		{"garbage after the object", `{"type":"user",` + sid + `} trailing`, 1},
		{"object without a type", `{` + sid + `,"value":1}`, 0},
		{"type that is not a string", `{"type":7,` + sid + `}`, 0},
		{"unknown type", `{"type":"brand-new-kind",` + sid + `,"payload":{"a":[1,{"b":"}"}]}}`, 0},
		{"system subtype that is not counted", `{"type":"system","subtype":"stop_hook_summary","uuid":"s1",` + sid + `}`, 0},
		{"attachment that is not a hook", `{"type":"attachment","uuid":"a1",` + sid + `,"attachment":{"type":"file","content":"x"}}`, 0},
		{"attachment without a body", `{"type":"attachment","uuid":"a2",` + sid + `}`, 0},
		{"assistant without a message", `{"type":"assistant","uuid":"x1",` + sid + `}`, 1},
		{"assistant message without an id", `{"type":"assistant","uuid":"x2",` + sid + `,"message":{"model":"m","content":[]}}`, 1},
		{"assistant message with broken json", `{"type":"assistant","uuid":"x3",` + sid + `,"message":{"id":"m1","content":[}]}}`, 1},
		{"tool use without an id", `{"type":"assistant","uuid":"x4",` + sid + `,"message":{"id":"m2","content":[{"type":"tool_use","name":"Bash"}]}}`, 1},
		{"user without content", `{"type":"user","uuid":"x5",` + sid + `,"message":{"role":"user"}}`, 1},
		{"user with a numeric content", `{"type":"user","uuid":"x6",` + sid + `,"message":{"content":5}}`, 1},
		{"tool result without an id", `{"type":"user","uuid":"x7",` + sid + `,"message":{"content":[{"type":"tool_result"}]}}`, 1},
		{"prompt without a uuid", `{"type":"user",` + sid + `,"message":{"content":"hello"}}`, 1},
		{"hook without a uuid", `{"type":"attachment",` + sid + `,"attachment":{"type":"hook_success"}}`, 1},
		{"turn end without a uuid", `{"type":"system","subtype":"turn_duration",` + sid + `,"durationMs":5}`, 1},
		{"compaction without a uuid", `{"type":"system","subtype":"compact_boundary",` + sid + `}`, 1},
		{"cost that is not a number", `{"type":"cost-state",` + sid + `,"totalCostUSD":"1.25"}`, 0},
	}
	follow := jsonl(t, promptLine("after", testLater, "still read"))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := append([]byte(tc.line+"\n"), follow...)
			got := mustScan(t, data, rootInput())
			if got.Skipped != tc.skip || got.Foreign != 0 || got.Lines != 2 {
				t.Errorf("skipped %d, foreign %d, lines %d; want %d, 0, 2", got.Skipped, got.Foreign, got.Lines, tc.skip)
			}
			// No line stops the scan: the one behind it is always read.
			if len(got.Prompts) != 1 || got.Prompts[0].Key != "after" || got.NextOffset != int64(len(data)) {
				t.Errorf("prompts %+v, offset %d; want the following prompt and offset %d", got.Prompts, got.NextOffset, len(data))
			}
			if got.Cost != nil {
				t.Errorf("Cost = %+v, want none", *got.Cost)
			}
		})
	}
}

// A line of a kind the scanner does not read still describes the session.
func TestScanUnknownKindContributesSessionAttributes(t *testing.T) {
	data := jsonl(t, with(base("brand-new-kind", "n1", testTime), obj{
		"cwd": "/work/other", "entrypoint": "sdk", "version": "9.9.9", "gitBranch": "feature", "payload": obj{"big": []int{1, 2, 3}},
	}))
	got := mustScan(t, data, rootInput())
	want := usage.Batch{
		Session: usage.SessionPatch{SessionID: testSession, ProjectPath: "/work/other", Entrypoint: "sdk",
			ClientVersion: "9.9.9", GitBranch: "feature", FirstAt: instant(t, testTime), LastAt: instant(t, testTime)},
		Lines:      1,
		NextOffset: int64(len(data)),
	}
	diff(t, got, want)
}

func TestScanInvalidUTF8(t *testing.T) {
	const bad = "\xff\xfe"
	lines := []string{
		// Invalid bytes inside values that are stored and measured.
		`{"type":"user","uuid":"p1","sessionId":"` + testSession + `","cwd":"/work/` + bad + `","promptSource":"typed","message":{"content":"a` + bad + `b"}}`,
		// Invalid bytes where JSON syntax is expected.
		bad + `{"type":"user"}`,
		// Invalid bytes in the session ID make the line foreign, not lost.
		`{"type":"user","uuid":"p2","sessionId":"` + bad + `","message":{"content":"x"}}`,
	}
	data := append([]byte(strings.Join(lines, "\n")+"\n"), jsonl(t, promptLine("p3", testTime, "ok"))...)
	got := mustScan(t, data, rootInput())
	if got.Lines != 4 || got.Skipped != 1 || got.Foreign != 1 || len(got.Prompts) != 2 {
		t.Fatalf("lines %d, skipped %d, foreign %d, prompts %+v; want 4, 1, 1 and two prompts", got.Lines, got.Skipped, got.Foreign, got.Prompts)
	}
	if !utf8.ValidString(got.Session.ProjectPath) || !strings.HasPrefix(got.Session.ProjectPath, "/work/") {
		t.Errorf("ProjectPath = %q, want valid UTF-8 starting with /work/", got.Session.ProjectPath)
	}
	// Each invalid byte decodes to one replacement character.
	if got.Prompts[0].Chars != len("ab")+len(bad) {
		t.Errorf("Chars = %d, want %d", got.Prompts[0].Chars, len("ab")+len(bad))
	}
}

func TestScanCRLF(t *testing.T) {
	unix := jsonl(t, promptLine("p1", testTime, "one"), promptLine("p2", testLater, "two"))
	dos := bytes.ReplaceAll(unix, []byte("\n"), []byte("\r\n"))
	want := mustScan(t, unix, rootInput())
	got := mustScan(t, dos, rootInput())
	if got.NextOffset != int64(len(dos)) {
		t.Fatalf("NextOffset = %d, want %d", got.NextOffset, len(dos))
	}
	want.NextOffset = got.NextOffset
	diff(t, got, want)
}

func TestScanForeignLines(t *testing.T) {
	foreign := with(promptLine("copied", "2026-01-01T00:00:00.000Z", "old"), obj{"sessionId": testOther, "cwd": "/work/elsewhere"})
	noSession := promptLine("bare", testTime, "mine")
	delete(noSession, "sessionId")
	data := jsonl(t, foreign, noSession, with(base("mode", "", ""), obj{"sessionId": testOther}))

	t.Run("filter on", func(t *testing.T) {
		got := mustScan(t, data, rootInput())
		if got.Foreign != 2 || got.Skipped != 0 || got.Lines != 3 {
			t.Fatalf("foreign %d, skipped %d, lines %d; want 2, 0, 3", got.Foreign, got.Skipped, got.Lines)
		}
		// A foreign line gives neither records nor session attributes.
		if len(got.Prompts) != 1 || got.Prompts[0].Key != "bare" || got.Prompts[0].SessionID != testSession {
			t.Fatalf("prompts = %+v, want only the line without a session ID", got.Prompts)
		}
		if got.Session.ProjectPath != "" || !got.Session.FirstAt.Equal(instant(t, testTime)) {
			t.Fatalf("session = %+v, want no path and the time of the stream's own line", got.Session)
		}
	})

	t.Run("filter off", func(t *testing.T) {
		got := mustScan(t, data, usage.ScanInput{})
		if got.Foreign != 0 || len(got.Prompts) != 2 {
			t.Fatalf("foreign %d, prompts %+v; want 0 and two prompts", got.Foreign, got.Prompts)
		}
		// Without a stream ID the records carry the ID of their line, or
		// the latest one seen for a line that has none.
		if got.Prompts[0].SessionID != testOther || got.Prompts[1].SessionID != testOther || got.Session.SessionID != testOther {
			t.Fatalf("session IDs = %q, %q, patch %q; want %q", got.Prompts[0].SessionID, got.Prompts[1].SessionID, got.Session.SessionID, testOther)
		}
	})
}

func TestScanModelCallMerge(t *testing.T) {
	block := func(uuid, at string, extra obj, message obj) obj {
		return with(base("assistant", uuid, at), with(extra, obj{"message": message}))
	}
	usageOf := func(out any) obj {
		return obj{"input_tokens": 10, "output_tokens": out, "cache_read_input_tokens": 300.0,
			"cache_creation_input_tokens": -4, "output_tokens_details": nil}
	}
	data := jsonl(t,
		block("b1", testTime, nil, obj{"id": "m1", "model": "model-small", "stop_reason": nil, "usage": usageOf(20), "content": []obj{{"type": "thinking", "thinking": "hidden"}}}),
		block("b2", testLater, obj{"attributionSkill": "review", "attributionPlugin": "kit"},
			obj{"id": "m1", "model": "model-small", "stop_reason": "tool_use", "usage": usageOf(25), "content": []obj{toolUse("t1", "Read", obj{"file_path": "/work/a"})}}),
		block("b3", testLater, nil, obj{"id": "m1", "model": "model-small", "usage": "unexpected", "content": "also unexpected"}),
		block("b4", "", nil, obj{"id": "m2", "content": []obj{toolUse("t2", "Task", obj{"subagent_type": "helper"})}}),
	)
	got := mustScan(t, data, usage.ScanInput{SessionID: testSession, AgentID: testAgent})
	wantCalls := []usage.ModelCall{
		{Key: "m1", SessionID: testSession, AgentID: testAgent, At: instant(t, testTime), Model: "model-small",
			Input: 10, Output: 25, CacheRead: 300, StopReason: "tool_use", Skill: "review", Plugin: "kit"},
		{Key: "m2", SessionID: testSession, AgentID: testAgent},
	}
	wantTools := []usage.ToolCall{
		{Key: "t1", SessionID: testSession, AgentID: testAgent, At: instant(t, testLater), Name: "Read", Category: usage.ToolBuiltin, MessageKey: "m1"},
		{Key: "t2", SessionID: testSession, AgentID: testAgent, Name: "Task", Category: usage.ToolAgent, SubagentType: "helper", MessageKey: "m2"},
	}
	diff(t, usage.Batch{ModelCalls: got.ModelCalls, ToolCalls: got.ToolCalls, Skipped: got.Skipped},
		usage.Batch{ModelCalls: wantCalls, ToolCalls: wantTools})
}

func TestScanPromptRules(t *testing.T) {
	text := func(s string) obj { return obj{"type": "text", "text": s} }
	message := func(content any) obj { return obj{"message": obj{"role": "user", "content": content}} }
	cases := []struct {
		name    string
		agent   string
		line    obj
		prompts int
		chars   int
		source  string
	}{
		{"string content", "", promptLine("p", testTime, "hello"), 1, 5, "typed"},
		{"counts characters, not bytes", "", promptLine("p", testTime, "héllo, 세계"), 1, 9, "typed"},
		{"empty string", "", promptLine("p", testTime, ""), 1, 0, "typed"},
		{"text blocks are added up", "", with(base("user", "p", testTime), message([]obj{text("ab"), text("cde")})), 1, 5, ""},
		{"text beside an image", "", with(base("user", "p", testTime), message([]obj{{"type": "image", "source": obj{}}, text("see")})), 1, 3, ""},
		{"image alone", "", with(base("user", "p", testTime), message([]obj{{"type": "image", "source": obj{}}})), 0, 0, ""},
		{"empty block list", "", with(base("user", "p", testTime), message([]obj{})), 0, 0, ""},
		{"meta line", "", with(promptLine("p", testTime, "caveat"), obj{"isMeta": true}), 0, 0, ""},
		{"meta flag that is off", "", with(promptLine("p", testTime, "hello"), obj{"isMeta": false}), 1, 5, "typed"},
		{"tool result line", "", resultLine("p", testTime, "t1"), 0, 0, ""},
		{"text beside a tool result", "", with(base("user", "p", testTime), message([]obj{text("note"), {"type": "tool_result", "tool_use_id": "t1"}})), 0, 0, ""},
		{"sub-agent string content", testAgent, promptLine("p", testTime, "task"), 0, 0, ""},
		{"sub-agent text block", testAgent, with(base("user", "p", testTime), message([]obj{text("task")})), 0, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustScan(t, jsonl(t, tc.line), usage.ScanInput{SessionID: testSession, AgentID: tc.agent})
			if got.Skipped != 0 || len(got.Prompts) != tc.prompts {
				t.Fatalf("skipped %d, prompts %+v; want 0 and %d", got.Skipped, got.Prompts, tc.prompts)
			}
			if tc.prompts == 0 {
				return
			}
			want := usage.Prompt{Key: "p", SessionID: testSession, At: instant(t, testTime), Source: tc.source, Chars: tc.chars}
			if got.Prompts[0] != want {
				t.Fatalf("prompt = %+v, want %+v", got.Prompts[0], want)
			}
		})
	}
}

func TestScanToolResultDetails(t *testing.T) {
	cases := []struct {
		name  string
		extra obj
		want  usage.ToolResult
	}{
		{"nothing reported", nil, usage.ToolResult{}},
		{"interrupted with a duration", obj{"toolUseResult": obj{"interrupted": true, "durationMs": 1234, "stdout": "x"}},
			usage.ToolResult{Interrupted: true, DurationMs: 1234}},
		{"fractional duration", obj{"toolUseResult": obj{"durationMs": 12.9}}, usage.ToolResult{DurationMs: 12}},
		{"negative duration", obj{"toolUseResult": obj{"durationMs": -5}}, usage.ToolResult{}},
		{"text result", obj{"toolUseResult": "Error: failed"}, usage.ToolResult{}},
		{"list result", obj{"toolUseResult": []obj{{"durationMs": 9}}}, usage.ToolResult{}},
		{"sandbox violations", obj{"sandboxViolations": []obj{{"kind": "file"}, {"kind": "network"}}}, usage.ToolResult{SandboxViolations: 2}},
		{"no sandbox violations", obj{"sandboxViolations": []obj{}}, usage.ToolResult{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := with(resultLine("r", testTime, "t1"), tc.extra)
			got := mustScan(t, jsonl(t, line), rootInput())
			want := tc.want
			want.Key, want.SessionID, want.At = "t1", testSession, instant(t, testTime)
			if got.Skipped != 0 || len(got.ToolResults) != 1 || got.ToolResults[0] != want {
				t.Fatalf("skipped %d, results %+v; want %+v", got.Skipped, got.ToolResults, want)
			}
		})
	}

	// is_error belongs to the block; a line can close several calls.
	two := with(base("user", "r2", testTime), obj{"message": obj{"content": []obj{
		{"type": "tool_result", "tool_use_id": "t1", "is_error": true},
		{"type": "tool_result", "tool_use_id": "t2", "is_error": nil},
	}}})
	got := mustScan(t, jsonl(t, two), rootInput())
	if len(got.ToolResults) != 2 || !got.ToolResults[0].IsError || got.ToolResults[1].IsError {
		t.Fatalf("results = %+v, want t1 failed and t2 not", got.ToolResults)
	}
}

func TestScanHookStatus(t *testing.T) {
	cases := []struct {
		kind string
		want usage.HookStatus
	}{
		{"hook_success", usage.HookSuccess},
		{"hook_non_blocking_error", usage.HookError},
		{"hook_cancelled", usage.HookError},
		{"hook_blocking_error", usage.HookBlocked},
		{"hook_additional_context", usage.HookUnknown},
		{"hook_success_later_variant", usage.HookUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			got := mustScan(t, jsonl(t, hookLine("h", testTime, tc.kind)), usage.ScanInput{SessionID: testSession, AgentID: testAgent})
			want := usage.HookRun{Key: "h", SessionID: testSession, AgentID: testAgent, At: instant(t, testTime),
				Event: "PreToolUse", Name: "PreToolUse:Bash", ToolUseKey: "toolu_x", Status: tc.want, DurationMs: 5}
			if len(got.Hooks) != 1 || got.Hooks[0] != want {
				t.Fatalf("hooks = %+v, want %+v", got.Hooks, want)
			}
		})
	}

	// A blocking error carries neither an exit code nor a duration.
	bare := with(base("attachment", "h2", testTime), obj{"attachment": obj{"type": "hook_blocking_error", "hookEvent": "Stop", "hookName": "Stop", "blockingError": obj{"command": "x"}}})
	got := mustScan(t, jsonl(t, bare), rootInput())
	want := usage.HookRun{Key: "h2", SessionID: testSession, At: instant(t, testTime), Event: "Stop", Name: "Stop", Status: usage.HookBlocked}
	if len(got.Hooks) != 1 || got.Hooks[0] != want {
		t.Fatalf("hooks = %+v, want %+v", got.Hooks, want)
	}
}

func TestScanSystemLines(t *testing.T) {
	turn := with(base("system", "turn", testTime), obj{"subtype": "turn_duration", "durationMs": 900, "messageCount": 4})
	compact := with(base("system", "compact", testLater), obj{"subtype": "compact_boundary",
		"compactMetadata": obj{"trigger": "manual", "preTokens": 12345, "postTokens": 100}})
	bare := with(base("system", "compact2", testLater), obj{"subtype": "compact_boundary"})
	data := jsonl(t, turn, compact, bare)

	root := mustScan(t, data, rootInput())
	diff(t, usage.Batch{Turns: root.Turns, Compactions: root.Compactions, Skipped: root.Skipped}, usage.Batch{
		Turns: []usage.TurnEnd{{Key: "turn", SessionID: testSession, At: instant(t, testTime), DurationMs: 900, Messages: 4}},
		Compactions: []usage.Compaction{
			{Key: "compact", SessionID: testSession, At: instant(t, testLater), Trigger: "manual", PreTokens: 12345},
			{Key: "compact2", SessionID: testSession, At: instant(t, testLater)},
		},
	})

	// TurnEnd has no agent field, so a sub-agent stream yields none.
	agent := mustScan(t, data, usage.ScanInput{SessionID: testSession, AgentID: testAgent})
	if len(agent.Turns) != 0 || len(agent.Compactions) != 2 || agent.Compactions[0].AgentID != testAgent || agent.Skipped != 0 {
		t.Fatalf("sub-agent: turns %+v, compactions %+v, skipped %d", agent.Turns, agent.Compactions, agent.Skipped)
	}
}

func TestScanCostState(t *testing.T) {
	cost := func(total any, extra obj) obj {
		return with(obj{"type": "cost-state", "sessionId": testSession, "totalCostUSD": total,
			"totalAPIDuration": 100, "totalToolDuration": 20, "totalLinesAdded": 3, "totalLinesRemoved": 1}, extra)
	}
	stamp := promptLine("p", testTime, "x")
	later := promptLine("q", testLater, "y")
	full := func(at time.Time, total float64) *usage.CostState {
		return &usage.CostState{SessionID: testSession, At: at, TotalCostUSD: total,
			APIDurationMs: 100, ToolDurationMs: 20, LinesAdded: 3, LinesRemoved: 1}
	}
	cases := []struct {
		name  string
		agent string
		lines []obj
		want  *usage.CostState
	}{
		{"takes the latest earlier time", "", []obj{stamp, later, cost(0.5, nil)}, full(instant(t, testLater), 0.5)},
		{"ignores later lines", "", []obj{stamp, cost(0.5, nil), later}, full(instant(t, testTime), 0.5)},
		{"first line of a batch has no time", "", []obj{cost(0.5, nil), stamp}, full(time.Time{}, 0.5)},
		{"own timestamp wins", "", []obj{stamp, cost(0.5, obj{"timestamp": testLater})}, full(instant(t, testLater), 0.5)},
		{"integer total", "", []obj{stamp, cost(2, nil)}, full(instant(t, testTime), 2)},
		{"zero is a reported cost", "", []obj{stamp, cost(0, nil)}, full(instant(t, testTime), 0)},
		{"last line wins", "", []obj{stamp, cost(0.5, nil), cost(0.75, nil)}, full(instant(t, testTime), 0.75)},
		{"non-numeric total keeps the earlier state", "", []obj{stamp, cost(0.5, nil), cost(nil, nil)}, full(instant(t, testTime), 0.5)},
		{"missing total", "", []obj{stamp, {"type": "cost-state", "sessionId": testSession, "totalAPIDuration": 100}}, nil},
		{"null total", "", []obj{stamp, cost(nil, nil)}, nil},
		{"boolean total", "", []obj{stamp, cost(true, nil)}, nil},
		{"sub-agent transcript", testAgent, []obj{stamp, cost(0.5, nil)}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mustScan(t, jsonl(t, tc.lines...), usage.ScanInput{SessionID: testSession, AgentID: tc.agent})
			if got.Skipped != 0 {
				t.Errorf("skipped = %d, want 0", got.Skipped)
			}
			diff(t, usage.Batch{Cost: got.Cost}, usage.Batch{Cost: tc.want})
		})
	}
}

func TestScanSessionPatch(t *testing.T) {
	t.Run("latest non-empty value and time range", func(t *testing.T) {
		data := jsonl(t,
			with(promptLine("a", testLater, "x"), obj{"cwd": "/work/one", "gitBranch": "main", "version": "1.0.0", "entrypoint": "cli"}),
			with(promptLine("b", testTime, "x"), obj{"cwd": "/work/two", "gitBranch": "", "version": nil}),
			with(promptLine("c", "not a time", "x"), obj{"entrypoint": 7}),
			obj{"type": "ai-title", "sessionId": testSession, "aiTitle": "First title"},
			obj{"type": "ai-title", "sessionId": testSession, "aiTitle": "Second title"},
			obj{"type": "ai-title", "sessionId": testSession, "aiTitle": ""},
			with(base("mode", "", ""), obj{"attributionAgent": "Explore"}),
		)
		got := mustScan(t, data, rootInput())
		want := usage.SessionPatch{SessionID: testSession, ProjectPath: "/work/two", Title: "Second title", Entrypoint: "cli",
			ClientVersion: "1.0.0", GitBranch: "main", FirstAt: instant(t, testTime), LastAt: instant(t, testLater)}
		if got.Session != want {
			t.Fatalf("session = %+v\nwant      %+v", got.Session, want)
		}
	})

	t.Run("agent type only for a sub-agent", func(t *testing.T) {
		data := jsonl(t, with(assistantLine("a", testTime, "m1"), obj{"attributionAgent": "Explore"}))
		if got := mustScan(t, data, rootInput()); got.Session.AgentType != "" {
			t.Errorf("root AgentType = %q, want empty", got.Session.AgentType)
		}
		got := mustScan(t, data, usage.ScanInput{SessionID: testSession, AgentID: testAgent})
		if got.Session.AgentType != "Explore" || got.Session.AgentID != testAgent {
			t.Errorf("sub-agent session = %+v, want type Explore", got.Session)
		}
	})

	t.Run("no line with a time", func(t *testing.T) {
		got := mustScan(t, jsonl(t, promptLine("a", "", "x")), rootInput())
		if !got.Session.FirstAt.IsZero() || !got.Session.LastAt.IsZero() || !got.Prompts[0].At.IsZero() {
			t.Errorf("times = %v, %v, %v; want all zero", got.Session.FirstAt, got.Session.LastAt, got.Prompts[0].At)
		}
	})

	t.Run("time zone is normalised", func(t *testing.T) {
		got := mustScan(t, jsonl(t, promptLine("a", "2026-04-02T19:00:00+09:00", "x")), rootInput())
		if !got.Session.FirstAt.Equal(instant(t, testTime)) || got.Session.FirstAt.Location() != time.UTC {
			t.Errorf("FirstAt = %v, want %s in UTC", got.Session.FirstAt, testTime)
		}
	})
}

func TestScanMasksAndCutsLabels(t *testing.T) {
	// Assembled at run time so that no secret-shaped literal is committed.
	secret := "gh" + "p_" + strings.Repeat("a1B2", 9)
	long := strings.Repeat("가", usage.MaxLabelLen+50)
	data := jsonl(t,
		obj{"type": "ai-title", "sessionId": testSession, "aiTitle": "Rotate " + secret + " now"},
		with(assistantLine("a", testTime, long, toolUse(long, long, nil)), obj{"cwd": long, "gitBranch": long, "attributionSkill": long}),
		with(resultLine("r", testTime, long), obj{"promptSource": long}),
		with(hookLine("h", testTime, "hook_success"), obj{"uuid": long}),
	)
	got := mustScan(t, data, rootInput())
	if strings.Contains(got.Session.Title, secret) || !strings.Contains(got.Session.Title, usage.MaskToken) {
		t.Errorf("Title = %q, want the secret replaced by %q", got.Session.Title, usage.MaskToken)
	}
	cut := strings.Repeat("가", usage.MaxLabelLen)
	checks := map[string]string{
		"ProjectPath":     got.Session.ProjectPath,
		"GitBranch":       got.Session.GitBranch,
		"ModelCall.Key":   got.ModelCalls[0].Key,
		"ModelCall.Skill": got.ModelCalls[0].Skill,
		"ToolCall.Key":    got.ToolCalls[0].Key,
		"ToolCall.Name":   got.ToolCalls[0].Name,
		"ToolCall.Msg":    got.ToolCalls[0].MessageKey,
		"ToolResult.Key":  got.ToolResults[0].Key,
		"HookRun.Key":     got.Hooks[0].Key,
	}
	for name, value := range checks {
		if value != cut {
			t.Errorf("%s has %d runes, want %d", name, utf8.RuneCountInString(value), usage.MaxLabelLen)
		}
	}

	// A title far beyond the stored length is cut before it is masked and
	// again after, so the stored title never exceeds the label length.
	huge := jsonl(t, obj{"type": "ai-title", "sessionId": testSession, "aiTitle": strings.Repeat("t", 100*usage.MaxLabelLen)})
	if n := utf8.RuneCountInString(mustScan(t, huge, rootInput()).Session.Title); n != usage.MaxLabelLen {
		t.Errorf("long title has %d runes, want %d", n, usage.MaxLabelLen)
	}
}

// failingReader returns its data and then an error that is not io.EOF.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestScanReadError(t *testing.T) {
	boom := errors.New("disk gone")
	first := jsonl(t, promptLine("p1", testTime, "one"))
	partial := []byte(`{"type":"user","uuid":"p2"`)

	got, err := Scan(&failingReader{data: append(append([]byte{}, first...), partial...), err: boom}, rootInput())
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the read error", err)
	}
	// The lines read before the failure are still reported.
	if got.NextOffset != int64(len(first)) || len(got.Prompts) != 1 || got.Lines != 1 {
		t.Fatalf("batch after a read error: offset %d, prompts %+v, lines %d", got.NextOffset, got.Prompts, got.Lines)
	}

	if _, err := Scan(iotest.ErrReader(boom), usage.ScanInput{Base: 7}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the read error", err)
	}
	if _, err := Scan(iotest.TimeoutReader(bytes.NewReader(first)), rootInput()); !errors.Is(err, iotest.ErrTimeout) {
		t.Fatalf("err = %v, want the timeout of the second read", err)
	}
	if _, err := Scan(io.LimitReader(bytes.NewReader(first), 5), rootInput()); err != nil {
		t.Fatalf("a short read that ends in EOF is not an error: %v", err)
	}
}
