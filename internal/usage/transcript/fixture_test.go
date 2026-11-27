package transcript

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/iotest"
	"time"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// The shared fixtures and the stream they belong to, as described in
// internal/usage/testdata/README.md.
const (
	fixtureDir     = "../testdata"
	fixtureRoot    = "session.jsonl"
	fixtureAgent   = "agent-a1b2c3.jsonl"
	fixtureSession = "11111111-1111-4111-8111-111111111111"
	fixtureAgentID = "a1b2c3"
	fixtureModel   = "model-large"
	fixtureDay     = "2026-03-01T"
)

// firstPromptText is the text of the first prompt of the root fixture; the
// test only needs its length.
const firstPromptText = "Find why the build fails. The log is in build.log."

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// clock returns a time of the fixture day, given as "09:00:08.000Z".
func clock(t *testing.T, hms string) time.Time {
	t.Helper()
	return instant(t, fixtureDay+hms)
}

func instant(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("bad test time %q: %v", value, err)
	}
	return parsed.UTC()
}

// mustScan runs Scan and fails the test on a read error.
func mustScan(t *testing.T, data []byte, in usage.ScanInput) usage.Batch {
	t.Helper()
	batch, err := Scan(bytes.NewReader(data), in)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	return batch
}

// diff reports every top-level field of the batch that differs, so that a
// failure names the record kind instead of dumping two whole batches.
func diff(t *testing.T, got, want usage.Batch) {
	t.Helper()
	g, w := reflect.ValueOf(got), reflect.ValueOf(want)
	for i := 0; i < g.NumField(); i++ {
		if !reflect.DeepEqual(g.Field(i).Interface(), w.Field(i).Interface()) {
			t.Errorf("Batch.%s\n got  %+v\n want %+v", g.Type().Field(i).Name, deref(g.Field(i)), deref(w.Field(i)))
		}
	}
}

func deref(v reflect.Value) any {
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		return v.Elem().Interface()
	}
	return v.Interface()
}

func wantRootBatch(t *testing.T, size int64) usage.Batch {
	t.Helper()
	const sid = fixtureSession
	model := func(key, hms string, in, out, read, create, thinking int64, stop string) usage.ModelCall {
		return usage.ModelCall{Key: key, SessionID: sid, At: clock(t, hms), Model: fixtureModel,
			Input: in, Output: out, CacheRead: read, CacheCreate: create, Thinking: thinking, StopReason: stop}
	}
	withAttribution := model("msg_04", "09:00:25.000Z", 6, 150, 51500, 80, 0, "tool_use")
	withAttribution.Skill, withAttribution.Plugin = "devkit:deploy-check", "devkit"
	result := func(key, hms string, isError bool) usage.ToolResult {
		return usage.ToolResult{Key: key, SessionID: sid, At: clock(t, hms), IsError: isError}
	}
	hook := func(key, hms, event, name, tool string, status usage.HookStatus, exit int, ms int64) usage.HookRun {
		return usage.HookRun{Key: key, SessionID: sid, At: clock(t, hms), Event: event, Name: name,
			ToolUseKey: tool, Status: status, ExitCode: exit, DurationMs: ms}
	}
	return usage.Batch{
		Session: usage.SessionPatch{
			SessionID: sid, ProjectPath: "/work/sample", Title: "Fix failing build", Entrypoint: "cli",
			ClientVersion: "2.1.329", GitBranch: "main",
			FirstAt: clock(t, "09:00:00.000Z"), LastAt: clock(t, "09:32:00.000Z"),
		},
		ModelCalls: []usage.ModelCall{
			model("msg_01", "09:00:08.000Z", 12, 420, 48000, 2100, 150, "tool_use"),
			model("msg_02", "09:00:15.000Z", 8, 610, 50100, 300, 0, "tool_use"),
			model("msg_03", "09:00:20.000Z", 5, 200, 51000, 100, 0, "tool_use"),
			withAttribution,
			model("msg_05", "09:00:30.000Z", 4, 90, 52000, 60, 0, "tool_use"),
			model("msg_06", "09:01:15.000Z", 3, 300, 52500, 40, 0, "end_turn"),
		},
		ToolCalls: []usage.ToolCall{
			{Key: "toolu_01", SessionID: sid, At: clock(t, "09:00:08.000Z"), Name: "Bash",
				Category: usage.ToolBuiltin, Target: "tail", MessageKey: "msg_01"},
			{Key: "toolu_02", SessionID: sid, At: clock(t, "09:00:15.000Z"), Name: "mcp__issues__search",
				Category: usage.ToolMCP, MCPServer: "issues", MCPTool: "search", MessageKey: "msg_02"},
			{Key: "toolu_03", SessionID: sid, At: clock(t, "09:00:20.000Z"), Name: "Skill",
				Category: usage.ToolSkill, Skill: "devkit:deploy-check", Plugin: "devkit", MessageKey: "msg_03"},
			{Key: "toolu_04", SessionID: sid, At: clock(t, "09:00:25.000Z"), Name: "mcp__plugin_devkit_chain__status",
				Category: usage.ToolMCP, MCPServer: "chain", MCPTool: "status", Plugin: "devkit", MessageKey: "msg_04"},
			{Key: "toolu_05", SessionID: sid, At: clock(t, "09:00:30.000Z"), Name: "Agent",
				Category: usage.ToolAgent, SubagentType: "Explore", MessageKey: "msg_05"},
		},
		ToolResults: []usage.ToolResult{
			result("toolu_01", "09:00:10.000Z", true),
			result("toolu_02", "09:00:17.000Z", false),
			result("toolu_03", "09:00:21.000Z", false),
			result("toolu_04", "09:00:27.000Z", false),
			result("toolu_05", "09:01:10.000Z", false),
		},
		Prompts: []usage.Prompt{
			{Key: "u-prompt-1", SessionID: sid, At: clock(t, "09:00:05.000Z"), Source: "typed",
				Chars: utf8.RuneCountInString(firstPromptText)},
			{Key: "u-prompt-2", SessionID: sid, At: clock(t, "09:32:00.000Z"), Source: "typed", Chars: len("Thanks.")},
		},
		Hooks: []usage.HookRun{
			hook("u-hook-1", "09:00:00.000Z", "SessionStart", "SessionStart:startup", "", usage.HookSuccess, 0, 120),
			hook("u-hook-2", "09:00:09.000Z", "PreToolUse", "PreToolUse:Bash", "toolu_01", usage.HookSuccess, 0, 31),
			hook("u-hook-3", "09:00:25.500Z", "PreToolUse", "PreToolUse:mcp__plugin_devkit_chain__status", "toolu_04", usage.HookError, 1, 900),
			hook("u-hook-4", "09:01:16.200Z", "Stop", "Stop", "", usage.HookSuccess, 0, 40),
		},
		Turns: []usage.TurnEnd{
			{Key: "u-turn-1", SessionID: sid, At: clock(t, "09:01:16.000Z"), DurationMs: 70000, Messages: 12},
		},
		Compactions: []usage.Compaction{
			{Key: "u-compact-1", SessionID: sid, At: clock(t, "09:30:00.000Z"), Trigger: "auto", PreTokens: 150000},
		},
		// The cost-state line has no time; the compaction before it is the
		// latest line that has one, because the line between them is broken.
		Cost: &usage.CostState{SessionID: sid, At: clock(t, "09:30:00.000Z"), TotalCostUSD: 1.25,
			APIDurationMs: 42000, ToolDurationMs: 4300, LinesAdded: 12, LinesRemoved: 3},
		Lines:      28,
		Skipped:    1,
		Foreign:    1,
		NextOffset: size,
	}
}

func TestScanRootFixture(t *testing.T) {
	data := readFixture(t, fixtureRoot)
	got := mustScan(t, data, usage.ScanInput{SessionID: fixtureSession})
	diff(t, got, wantRootBatch(t, int64(len(data))))

	var sum usage.ModelCall
	for _, call := range got.ModelCalls {
		sum.Input += call.Input
		sum.Output += call.Output
		sum.CacheRead += call.CacheRead
		sum.CacheCreate += call.CacheCreate
		sum.Thinking += call.Thinking
	}
	want := usage.ModelCall{Input: 38, Output: 1770, CacheRead: 305100, CacheCreate: 2680, Thinking: 150}
	if sum != want {
		t.Errorf("token sums = %+v, want %+v", sum, want)
	}
}

func TestScanAgentFixture(t *testing.T) {
	data := readFixture(t, fixtureAgent)
	const sid, aid = fixtureSession, fixtureAgentID
	got := mustScan(t, data, usage.ScanInput{SessionID: sid, AgentID: aid})
	want := usage.Batch{
		Session: usage.SessionPatch{
			SessionID: sid, AgentID: aid, AgentType: "Explore", ProjectPath: "/work/sample", Entrypoint: "cli",
			ClientVersion: "2.1.329", GitBranch: "main",
			FirstAt: clock(t, "09:00:31.000Z"), LastAt: clock(t, "09:01:05.000Z"),
		},
		ModelCalls: []usage.ModelCall{
			{Key: "msg_a1", SessionID: sid, AgentID: aid, At: clock(t, "09:00:35.000Z"), Model: fixtureModel,
				Input: 10, Output: 80, CacheRead: 9000, CacheCreate: 500, StopReason: "tool_use"},
			{Key: "msg_a2", SessionID: sid, AgentID: aid, At: clock(t, "09:01:05.000Z"), Model: fixtureModel,
				Input: 4, Output: 120, CacheRead: 9500, CacheCreate: 20, StopReason: "end_turn"},
		},
		ToolCalls: []usage.ToolCall{
			{Key: "toolu_a1", SessionID: sid, AgentID: aid, At: clock(t, "09:00:35.000Z"), Name: "WebFetch",
				Category: usage.ToolBuiltin, MessageKey: "msg_a1"},
		},
		ToolResults: []usage.ToolResult{
			{Key: "toolu_a1", SessionID: sid, At: clock(t, "09:00:37.000Z"), DurationMs: 850},
		},
		Hooks: []usage.HookRun{
			{Key: "s-hook-1", SessionID: sid, AgentID: aid, At: clock(t, "09:00:35.200Z"), Event: "PreToolUse",
				Name: "PreToolUse:WebFetch", ToolUseKey: "toolu_a1", Status: usage.HookSuccess, DurationMs: 20},
		},
		Lines:      5,
		NextOffset: int64(len(data)),
	}
	diff(t, got, want)
	if len(got.Prompts) != 0 {
		t.Errorf("a sub-agent transcript must not yield prompts, got %d", len(got.Prompts))
	}
}

// The result must not depend on how the reader hands out its bytes.
func TestScanFixtureAnyReadSize(t *testing.T) {
	data := readFixture(t, fixtureRoot)
	want := wantRootBatch(t, int64(len(data)))
	readers := map[string]func(io.Reader) io.Reader{
		"one byte":     iotest.OneByteReader,
		"half":         iotest.HalfReader,
		"data and eof": iotest.DataErrReader,
	}
	for name, wrap := range readers {
		t.Run(name, func(t *testing.T) {
			got, err := Scan(wrap(bytes.NewReader(data)), usage.ScanInput{SessionID: fixtureSession})
			if err != nil {
				t.Fatalf("Scan: %v", err)
			}
			diff(t, got, want)
		})
	}
}

// Resuming from NextOffset must read every line exactly once, whatever the
// chunk size.
func TestScanFixtureResume(t *testing.T) {
	data := readFixture(t, fixtureRoot)
	whole := wantRootBatch(t, int64(len(data)))
	for _, chunk := range []int64{1, 300, 1024, 4096, int64(len(data)), int64(len(data)) + 1} {
		var merged usage.Batch
		seenCalls := map[string]bool{}
		offset := int64(0)
		for rounds := 0; offset < int64(len(data)); rounds++ {
			if rounds > len(data) {
				t.Fatalf("chunk %d: scan does not advance", chunk)
			}
			batch, err := Scan(bytes.NewReader(data[offset:]), usage.ScanInput{SessionID: fixtureSession, Base: offset, MaxBytes: chunk})
			if err != nil {
				t.Fatalf("chunk %d: Scan: %v", chunk, err)
			}
			if batch.BaseOffset != offset || batch.NextOffset <= offset {
				t.Fatalf("chunk %d: offsets %d..%d after %d", chunk, batch.BaseOffset, batch.NextOffset, offset)
			}
			if data[batch.NextOffset-1] != '\n' {
				t.Fatalf("chunk %d: NextOffset %d is not a line boundary", chunk, batch.NextOffset)
			}
			if read := batch.NextOffset - offset; read < chunk && batch.NextOffset != int64(len(data)) {
				t.Fatalf("chunk %d: stopped after %d bytes before the end", chunk, read)
			}
			offset = batch.NextOffset
			for _, call := range batch.ModelCalls {
				// A message split by a chunk boundary is reported by both
				// batches under the same key, which the store collapses.
				if !seenCalls[call.Key] {
					seenCalls[call.Key] = true
					merged.ModelCalls = append(merged.ModelCalls, call)
				}
			}
			merged.ToolCalls = append(merged.ToolCalls, batch.ToolCalls...)
			merged.ToolResults = append(merged.ToolResults, batch.ToolResults...)
			merged.Prompts = append(merged.Prompts, batch.Prompts...)
			merged.Hooks = append(merged.Hooks, batch.Hooks...)
			merged.Turns = append(merged.Turns, batch.Turns...)
			merged.Compactions = append(merged.Compactions, batch.Compactions...)
			merged.Lines += batch.Lines
			merged.Skipped += batch.Skipped
			merged.Foreign += batch.Foreign
		}
		if len(merged.ModelCalls) != len(whole.ModelCalls) {
			t.Errorf("chunk %d: %d model calls, want %d", chunk, len(merged.ModelCalls), len(whole.ModelCalls))
		}
		// Model calls, the session patch and the cost are per-batch views
		// and are covered by the single-pass test.
		merged.ModelCalls, merged.Session, merged.Cost, merged.NextOffset = whole.ModelCalls, whole.Session, whole.Cost, whole.NextOffset
		diff(t, merged, whole)
	}
}
