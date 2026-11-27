package transcript

import (
	"bytes"
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// fuzzLimits keeps oversized lines within reach of small fuzz inputs.
func fuzzLimits() limits {
	return limits{line: 256, sniff: 48}
}

// checkStrings fails when a string anywhere in v is not valid UTF-8 or is
// longer than a stored label may be.
func checkStrings(t *testing.T, v reflect.Value, path string) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		s := v.String()
		if !utf8.ValidString(s) {
			t.Errorf("%s is not valid UTF-8: %q", path, s)
		}
		if n := utf8.RuneCountInString(s); n > usage.MaxLabelLen {
			t.Errorf("%s has %d runes, more than %d", path, n, usage.MaxLabelLen)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				checkStrings(t, v.Field(i), path+"."+v.Type().Field(i).Name)
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			checkStrings(t, v.Index(i), path)
		}
	case reflect.Pointer:
		if !v.IsNil() {
			checkStrings(t, v.Elem(), path)
		}
	}
}

// checkBatch asserts what must hold for any input: the scan stays inside the
// bytes it was given, stops on a line boundary, and accounts for each line at
// most once.
func checkBatch(t *testing.T, data []byte, in usage.ScanInput, b usage.Batch) int64 {
	t.Helper()
	read := b.NextOffset - in.Base
	if b.BaseOffset != in.Base || read < 0 || read > int64(len(data)) {
		t.Fatalf("offsets %d..%d for %d bytes from %d", b.BaseOffset, b.NextOffset, len(data), in.Base)
	}
	if read > 0 && data[read-1] != '\n' {
		t.Fatalf("NextOffset %d is not behind a newline", b.NextOffset)
	}
	if lines := bytes.Count(data[:read], []byte("\n")); b.Lines != lines {
		t.Fatalf("Lines = %d, but %d lines were consumed", b.Lines, lines)
	}
	if b.Skipped < 0 || b.Foreign < 0 || b.Skipped+b.Foreign > b.Lines {
		t.Fatalf("skipped %d + foreign %d exceed %d lines", b.Skipped, b.Foreign, b.Lines)
	}
	if (in.MaxBytes <= 0 || read < in.MaxBytes) && bytes.IndexByte(data[read:], '\n') >= 0 {
		t.Fatalf("stopped after %d bytes with MaxBytes %d and complete lines left", read, in.MaxBytes)
	}
	if in.SessionID != "" && b.Session.SessionID != in.SessionID {
		t.Fatalf("session ID %q, want the stream's %q", b.Session.SessionID, in.SessionID)
	}
	if in.AgentID != "" && (len(b.Prompts) != 0 || len(b.Turns) != 0 || b.Cost != nil) {
		t.Fatalf("sub-agent batch has prompts %d, turns %d, cost %v", len(b.Prompts), len(b.Turns), b.Cost)
	}
	keys := map[string]bool{}
	for _, call := range b.ModelCalls {
		if call.Key == "" || keys[call.Key] {
			t.Fatalf("model call key %q is empty or repeated in one batch", call.Key)
		}
		keys[call.Key] = true
	}
	checkStrings(t, reflect.ValueOf(b), "Batch")
	return read
}

func FuzzScan(f *testing.F) {
	for _, name := range []string{fixtureRoot, fixtureAgent} {
		data := readFixture(f, name)
		f.Add(data, int64(0), false)
		f.Add(data, int64(700), true)
	}
	seeds := []string{
		"",
		"\n\n\r\n",
		"not json\n",
		`{"type":"user","sessionId":"s","uuid":"u","message":{"content":"hi"}}` + "\n",
		`{"type":"user","sessionId":"s","uuid":"u","message":{"content":[{"type":"tool_result","tool_use_id":"t","is_error":true}]},"toolUseResult":{"durationMs":3},"sandboxViolations":[1]}` + "\n",
		`{"type":"assistant","message":{"id":"m","usage":{"input_tokens":1e30},"content":[{"type":"tool_use","id":"t","name":"Bash","input":{"command":"sudo -u x 'a b' | c"}}]}}` + "\n",
		`{"type":"assistant","message":{"id":"m","content":[{"type":"tool_use","id":"t","name":"mcp__plugin_a_b__c"}]}}` + "\n",
		`{"type":"attachment","uuid":"h","attachment":{"type":"hook_cancelled","exitCode":-1,"durationMs":1.5}}` + "\n",
		`{"type":"system","subtype":"compact_boundary","uuid":"c","compactMetadata":{"trigger":"auto","preTokens":9}}` + "\n",
		`{"type":"cost-state","totalCostUSD":0.1}` + "\n" + `{"type":"ai-title","aiTitle":"t"}` + "\n",
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","message":{"content":"` + string(bytes.Repeat([]byte("x"), 600)) + `"}}` + "\n{}\n",
		"{\"a\":\"\\\\\\\"\",\"type\":\"user\"\n[{}]\n{\"type\":\"user\"",
		"{\"type\":\"user\",\"cwd\":\"\xff\xfe\",\"uuid\":\"\\ud800\",\"message\":{\"content\":\"\xc3\"}}\n",
	}
	for _, seed := range seeds {
		f.Add([]byte(seed), int64(0), false)
		f.Add([]byte(seed), int64(40), true)
	}
	f.Fuzz(func(t *testing.T, data []byte, maxBytes int64, agent bool) {
		in := usage.ScanInput{SessionID: fixtureSession, Base: 1 << 20, MaxBytes: maxBytes}
		if agent {
			in.AgentID = fixtureAgentID
		}
		whole, err := Scan(bytes.NewReader(data), in)
		if err != nil {
			t.Fatalf("Scan failed on an in-memory reader: %v", err)
		}
		checkBatch(t, data, in, whole)

		// The same input in pieces, with small limits and no session filter:
		// every line must be accounted for exactly once across the calls.
		in.SessionID = ""
		var lines, offset int
		for offset < len(data) {
			in.Base = int64(offset)
			part, err := scan(bytes.NewReader(data[offset:]), in, fuzzLimits())
			if err != nil {
				t.Fatalf("scan failed on an in-memory reader: %v", err)
			}
			read := checkBatch(t, data[offset:], in, part)
			if part.Foreign != 0 {
				t.Fatalf("Foreign = %d without a session filter", part.Foreign)
			}
			if read == 0 {
				break
			}
			lines += part.Lines
			offset += int(read)
		}
		if want := bytes.Count(data, []byte("\n")); lines != want {
			t.Fatalf("pieces consumed %d lines, input has %d", lines, want)
		}
	})
}

func FuzzProgram(f *testing.F) {
	for _, seed := range []string{
		"git status", "A=1; B=$(pwd) sudo -u x env C=2 /usr/bin/make all", "X=`a` \\\n 'b c' | d",
		"sudo -Eu", "((((", "$($($(", "\\", "'", "\"", "a=\"b", "\x00\xff=1 \xff",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, command string) {
		name := program(command)
		if len(name) > maxProgramLen {
			t.Fatalf("program(%q) = %q, longer than %d", command, name, maxProgramLen)
		}
		for i := 0; i < len(name); i++ {
			if !programChar(name[i]) {
				t.Fatalf("program(%q) = %q, with a character outside the allowed set", command, name)
			}
		}
	})
}
