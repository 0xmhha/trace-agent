package timeline

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func encodeRaw(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

// secrets holds secret-shaped samples. They are assembled at run time so
// that no secret-shaped literal is committed to the repository.
type secrets struct {
	hostingToken string
	modelKey     string
	password     string
	spaced       string
	assignment   string
	exportQuoted string
}

func newSecrets() secrets {
	filler := strings.Repeat("A", 24)
	s := secrets{
		hostingToken: "gh" + "p_" + filler,
		modelKey:     "s" + "k-" + strings.Repeat("b", 24),
		password:     "hunter" + "2222",
		spaced:       "abc def " + "ghi",
	}
	s.assignment = "DB_PASS" + "WORD=" + s.password
	s.exportQuoted = "export API_TO" + "KEN=\"" + s.spaced + "\" && deploy"
	return s
}

func (s secrets) all() []string {
	return []string{s.hostingToken, s.modelKey, s.password, s.spaced}
}

// assertNoSecret fails when any sample survived in text.
func assertNoSecret(t *testing.T, where, text string, s secrets) {
	t.Helper()
	for _, secret := range s.all() {
		if strings.Contains(text, secret) {
			t.Errorf("%s still holds a secret sample: %q", where, text)
		}
	}
}

func TestReadMasksSecrets(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	s := newSecrets()
	secretKey := "api" + "_key"
	data := transcript(t,
		promptLine("p1", at, "use "+s.hostingToken+" for the upload"),
		assistantLine("a1", at, "msg_1",
			obj{"type": "text", "text": "Setting " + s.assignment + " now."},
			toolUse("t1", "Bash", obj{
				"command":     s.exportQuoted,
				secretKey:     s.modelKey,
				"description": "deploy with " + s.assignment,
				"env":         obj{"client_" + "secret": obj{"value": s.password}, "region": "local"},
				"retries":     3,
			}),
		),
		resultLine("r1", at, toolResult("t1", "ok\n"+s.assignment+"\ndone")),
		assistantLine("a2", at, "msg_2", toolUse("t2", "Read", obj{"file_path": "notes.txt"})),
		resultLine("r2", at, toolResult("t2", []obj{{"type": "text", "text": "line one"}, {"type": "text", "text": "key " + s.hostingToken}})),
	)

	items, _, _ := readAll(t, data, usage.TimelineRequest{Limit: 1})
	if len(items) != 6 {
		t.Fatalf("items = %d; want 6", len(items))
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("encode items: %v", err)
	}
	assertNoSecret(t, "the encoded page", string(encoded), s)

	for _, tc := range []struct {
		index  int
		name   string
		text   string
		masked int
	}{
		{0, "prompt", "use " + usage.MaskToken + " for the upload", 1},
		{1, "assistant text", "Setting DB_PASS" + "WORD=" + usage.MaskToken + " now.", 1},
		{3, "string tool result", "ok\nDB_PASS" + "WORD=" + usage.MaskToken + "\ndone", 1},
		{5, "block tool result", "line one\nkey " + usage.MaskToken, 1},
	} {
		if it := items[tc.index]; it.Text != tc.text || it.Masked != tc.masked {
			t.Errorf("%s: Text = %q, Masked = %d; want %q, %d", tc.name, it.Text, it.Masked, tc.text, tc.masked)
		}
	}

	call := items[2]
	wantInput := strings.Join([]string{
		"{",
		`  "` + secretKey + `": "` + usage.MaskToken + `",`,
		`  "command": "export API_TO` + `KEN=` + usage.MaskToken + ` && deploy",`,
		`  "description": "deploy with DB_PASS` + `WORD=` + usage.MaskToken + `",`,
		`  "env": {`,
		`    "client_` + `secret": "` + usage.MaskToken + `",`,
		`    "region": "local"`,
		`  },`,
		`  "retries": 3`,
		"}",
	}, "\n")
	if call.Tool.Input != wantInput {
		t.Errorf("tool input:\n got %s\nwant %s", call.Tool.Input, wantInput)
	}
	if call.Masked != 4 || call.Truncated {
		t.Errorf("tool call: Masked = %d, Truncated = %v; want 4, false", call.Masked, call.Truncated)
	}
	if call.Text != "" {
		t.Errorf("tool call carries text %q; the input belongs in Tool.Input", call.Text)
	}
	if untouched := items[4]; untouched.Masked != 0 || untouched.Tool.Input != "{\n  \"file_path\": \"notes.txt\"\n}" {
		t.Errorf("plain tool call: Masked = %d, Input = %q; want it unchanged", untouched.Masked, untouched.Tool.Input)
	}
}

func TestMaskInput(t *testing.T) {
	s := newSecrets()
	tokenKey := "to" + "ken"
	for _, tc := range []struct {
		name      string
		raw       string
		want      string
		masked    int
		truncated bool
	}{
		{"absent", ``, "", 0, false},
		{"null", ` null `, "", 0, false},
		{"empty object", `{}`, "{}", 0, false},
		{"empty array", `[]`, "[]", 0, false},
		{"nested empties", `{"a":{},"b":[]}`, "{\n  \"a\": {},\n  \"b\": []\n}", 0, false},
		{"key order is kept", `{"zeta":1,"alpha":2}`, "{\n  \"zeta\": 1,\n  \"alpha\": 2\n}", 0, false},
		{"scalars", `[true,false,null,1.50,-2e3,"x"]`, "[\n  true,\n  false,\n  null,\n  1.50,\n  -2e3,\n  \"x\"\n]", 0, false},
		{"large integer keeps its digits", `{"n":12345678901234567890}`, "{\n  \"n\": 12345678901234567890\n}", 0, false},
		{"no HTML escaping", `{"command":"a && b > c"}`, "{\n  \"command\": \"a && b > c\"\n}", 0, false},
		{"bare string", `"plain"`, `"plain"`, 0, false},
		{"secret in a bare string", `"` + s.hostingToken + `"`, `"` + usage.MaskToken + `"`, 1, false},
		{"secret in an array", `["x","` + s.modelKey + `"]`, "[\n  \"x\",\n  \"" + usage.MaskToken + "\"\n]", 1, false},
		{"secret key hides a string", `{"` + tokenKey + `":"plain words"}`, "{\n  \"" + tokenKey + "\": \"" + usage.MaskToken + "\"\n}", 1, false},
		{"secret key hides an object", `{"` + tokenKey + `":{"a":"b"}}`, "{\n  \"" + tokenKey + "\": \"" + usage.MaskToken + "\"\n}", 1, false},
		{"secret key hides an array", `{"` + tokenKey + `":["a","b"],"z":1}`, "{\n  \"" + tokenKey + "\": \"" + usage.MaskToken + "\",\n  \"z\": 1\n}", 1, false},
		{"secret key keeps literals", `{"` + tokenKey + `":42,"pass` + `word":true,"sec` + `ret":null,"api_` + `key":""}`,
			"{\n  \"" + tokenKey + "\": 42,\n  \"pass" + "word\": true,\n  \"sec" + "ret\": null,\n  \"api_" + "key\": \"\"\n}", 0, false},
		{"counter keys stay readable", `{"max_tokens":4096,"token_count":"12"}`, "{\n  \"max_tokens\": 4096,\n  \"token_count\": \"12\"\n}", 0, false},
		{"escaped quotes", `{"command":"export API_TO` + `KEN=\"` + s.spaced + `\""}`, "{\n  \"command\": \"export API_TO" + "KEN=" + usage.MaskToken + "\"\n}", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated, masked := maskInput(json.RawMessage(tc.raw))
			if got != tc.want || masked != tc.masked || truncated != tc.truncated {
				t.Errorf("maskInput(%s) = %q, %v, %d; want %q, %v, %d", tc.raw, got, truncated, masked, tc.want, tc.truncated, tc.masked)
			}
			assertNoSecret(t, "the input", got, s)
		})
	}
}

func TestMaskInputBounds(t *testing.T) {
	long := strings.Repeat("word ", usage.MaxMaskInput)
	manyKeys := obj{}
	for i := range 4 * usage.MaxTimelineText / 8 {
		manyKeys["key"+itoa(int64(i))] = "v"
	}
	deep := strings.Repeat("[", 5000) + strings.Repeat("]", 5000)
	for _, tc := range []struct {
		name  string
		input any
	}{
		{"one huge string", obj{"content": long}},
		{"huge string after a small one", obj{"a": "small", "content": long}},
		{"many small values", manyKeys},
		{"many array elements", obj{"items": strings.Split(strings.Repeat("x,", usage.MaxTimelineText), ",")}},
		{"deep nesting", json.RawMessage(deep)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.input)
			if err != nil {
				t.Fatalf("encode input: %v", err)
			}
			got, truncated, _ := maskInput(raw)
			if !truncated {
				t.Error("Truncated is false; want true for an input beyond the display limit")
			}
			if len(got) > usage.MaxTimelineText || len(got) < usage.MaxTimelineText/2 {
				t.Errorf("len = %d; want it close to and not above %d", len(got), usage.MaxTimelineText)
			}
			if !utf8.ValidString(got) {
				t.Error("the cut input is not valid UTF-8")
			}
		})
	}
}

func TestReadTruncatesLongText(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	s := newSecrets()
	// Multi-byte text, so that a cut in the middle of a character would show.
	korean := strings.Repeat("가나다 ", usage.MaxMaskInput)
	// The secret sits past the masking input limit and must simply be cut off.
	tail := strings.Repeat("line of output\n", usage.MaxMaskInput/8) + s.assignment
	blocks := make([]obj, 0, 64)
	for range 64 {
		blocks = append(blocks, obj{"type": "text", "text": strings.Repeat("block ", 200)})
	}
	data := transcript(t,
		promptLine("p1", at, korean),
		assistantLine("a1", at, "msg_1",
			obj{"type": "text", "text": korean},
			toolUse("t1", "Write", obj{"file_path": "big.txt", "content": korean}),
		),
		resultLine("r1", at, toolResult("t1", tail)),
		assistantLine("a2", at, "msg_2", toolUse("t2", "Read", obj{})),
		resultLine("r2", at, toolResult("t2", blocks)),
		promptLine("p2", at, "short"),
	)
	items, _, _ := readAll(t, data, usage.TimelineRequest{})
	if len(items) != 7 {
		t.Fatalf("items = %d; want 7", len(items))
	}
	for _, index := range []int{0, 1, 2, 3, 5} {
		it := items[index]
		text := it.Text
		if it.Kind == usage.ItemToolCall {
			text = it.Tool.Input
		}
		if !it.Truncated {
			t.Errorf("%s: Truncated is false; want true", it.ID)
		}
		if len(text) > usage.MaxTimelineText || len(text) < usage.MaxTimelineText-utf8.UTFMax {
			t.Errorf("%s: len = %d; want just under %d", it.ID, len(text), usage.MaxTimelineText)
		}
		if !utf8.ValidString(text) {
			t.Errorf("%s: the cut text is not valid UTF-8", it.ID)
		}
		assertNoSecret(t, it.ID, text, s)
	}
	for _, index := range []int{4, 6} {
		if it := items[index]; it.Truncated {
			t.Errorf("%s: Truncated is true for short content", it.ID)
		}
	}
	if !strings.HasPrefix(items[2].Tool.Input, "{\n  \"content\": \"가나다 ") {
		t.Errorf("cut tool input does not start with its first key: %.60q", items[2].Tool.Input)
	}
}

// A secret that masking shortens must not pull uncut text into view: the
// result is still bounded and still masked.
func TestDisplayText(t *testing.T) {
	s := newSecrets()
	for _, tc := range []struct {
		name      string
		in        string
		want      string
		truncated bool
		masked    int
	}{
		{"empty", "", "", false, 0},
		{"short", "go test passed", "go test passed", false, 0},
		{"masked", "key " + s.modelKey, "key " + usage.MaskToken, false, 1},
		{"exactly the display limit", strings.Repeat("a", usage.MaxTimelineText), strings.Repeat("a", usage.MaxTimelineText), false, 0},
		{"one over the display limit", strings.Repeat("a", usage.MaxTimelineText+1), strings.Repeat("a", usage.MaxTimelineText), true, 0},
		{"over the masking limit", strings.Repeat("a", usage.MaxMaskInput+1), strings.Repeat("a", usage.MaxTimelineText), true, 0},
		{"secret at the start of long text", s.modelKey + strings.Repeat(" b", usage.MaxTimelineText), usage.MaskToken + strings.Repeat(" b", (usage.MaxTimelineText-len(usage.MaskToken))/2), true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated, masked := displayText(tc.in)
			if got != tc.want || truncated != tc.truncated || masked != tc.masked {
				t.Errorf("displayText = %.40q (len %d), %v, %d; want %.40q (len %d), %v, %d",
					got, len(got), truncated, masked, tc.want, len(tc.want), tc.truncated, tc.masked)
			}
		})
	}
}

// Text is cut before it is masked. A large masked block at the start shows
// the order: what follows the first cut never comes back into view, however
// much room the masking frees.
func TestDisplayTextCutsBeforeMasking(t *testing.T) {
	const (
		bodyLen = usage.MaxMaskInput - 2048
		tailLen = usage.MaxMaskInput
	)
	begin := "-----BEGIN " + "PRIV" + "ATE KEY-----\n"
	end := "-----END " + "PRIV" + "ATE KEY-----"
	block := begin + strings.Repeat("k", bodyLen) + "\n" + end
	text := block + strings.Repeat("t", tailLen)

	got, truncated, masked := displayText(text)
	want := usage.MaskToken + strings.Repeat("t", usage.MaxMaskInput-len(block))
	if got != want || !truncated || masked != 1 {
		t.Errorf("displayText = %d bytes, %v, %d; want %d bytes (the mask token and the tail inside the first cut), true, 1",
			len(got), truncated, masked, len(want))
	}
}

// Joining the blocks of a tool result stops at the masking limit, and the
// item still says that text was cut when masking makes the rest short.
func TestReadJoinedResultIsMarkedTruncated(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	block := "-----BEGIN " + "PRIV" + "ATE KEY-----\n" + strings.Repeat("k", usage.MaxMaskInput-2048) + "\n-----END " + "PRIV" + "ATE KEY-----"
	data := transcript(t,
		assistantLine("a1", at, "msg_1", toolUse("t1", "Read", obj{})),
		resultLine("r1", at, toolResult("t1", []obj{
			{"type": "text", "text": block},
			{"type": "text", "text": strings.Repeat("t", usage.MaxMaskInput)},
		})),
	)
	items, _, _ := readAll(t, data, usage.TimelineRequest{})
	result := items[1]
	want := usage.MaskToken + "\n" + strings.Repeat("t", usage.MaxMaskInput-len(block)-1)
	if result.Text != want || !result.Truncated || result.Masked != 1 {
		t.Errorf("result = %d bytes, Truncated %v, Masked %d; want %d bytes, true, 1", len(result.Text), result.Truncated, result.Masked, len(want))
	}
}

// The writer stops soon after the display limit instead of re-encoding all
// of a huge input.
func TestInputWriterStopsEarly(t *testing.T) {
	values := make([]string, 64*usage.MaxTimelineText)
	for i := range values {
		values[i] = "v"
	}
	raw, err := json.Marshal(obj{"items": values})
	if err != nil {
		t.Fatalf("encode input: %v", err)
	}
	w := newInputWriter(raw)
	if err := w.value("", 0); !errors.Is(err, errInputFull) {
		t.Fatalf("err = %v; want errInputFull", err)
	}
	if w.out.Len() > 2*usage.MaxTimelineText {
		t.Errorf("wrote %d bytes of %d before stopping; want to stop near %d", w.out.Len(), len(raw), usage.MaxTimelineText)
	}
}

func TestClipAndLabel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		limit int
		want  string
		cut   bool
	}{
		{"shorter", "abc", 5, "abc", false},
		{"exact", "abcde", 5, "abcde", false},
		{"ascii", "abcdef", 5, "abcde", true},
		{"between characters", "가나다", 6, "가나", true},
		{"inside a character", "가나다", 5, "가", true},
		{"inside the first character", "가나다", 2, "", true},
		{"zero limit", "abc", 0, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, cut := clip(tc.in, tc.limit)
			if got != tc.want || cut != tc.cut {
				t.Errorf("clip(%q, %d) = %q, %v; want %q, %v", tc.in, tc.limit, got, cut, tc.want, tc.cut)
			}
		})
	}

	exact := strings.Repeat("가", usage.MaxLabelLen)
	for name, tc := range map[string]struct{ in, want string }{
		"short":      {"Bash", "Bash"},
		"exact":      {exact, exact},
		"long":       {exact + "나다", exact},
		"long ascii": {strings.Repeat("n", usage.MaxLabelLen+30), strings.Repeat("n", usage.MaxLabelLen)},
	} {
		if got := label(tc.in); got != tc.want {
			t.Errorf("label %s: got %d characters; want %d", name, utf8.RuneCountInString(got), utf8.RuneCountInString(tc.want))
		}
	}
}

// Names from the transcript are capped too, and a capped tool name still
// links a result to its call.
func TestReadCapsLabels(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	longName := strings.Repeat("N", usage.MaxLabelLen+50)
	data := transcript(t,
		assistantLine("a1", at, "msg_1", toolUse("t1", longName, obj{})),
		resultLine("r1", at, toolResult("t1", "ok")),
		obj{"type": "attachment", "uuid": "h1", "timestamp": at, "attachment": obj{
			"type": "hook_cancelled", "hookEvent": longName, "hookName": longName, "exitCode": 1e12, "durationMs": -4,
		}},
	)
	items, _, _ := readAll(t, data, usage.TimelineRequest{Limit: 1})
	want := longName[:usage.MaxLabelLen]
	if items[0].Tool.Name != want || items[1].Tool.Name != want {
		t.Errorf("tool names are %d and %d characters; want %d", len(items[0].Tool.Name), len(items[1].Tool.Name), usage.MaxLabelLen)
	}
	hook := items[2].Hook
	if hook.Event != want || hook.Name != want || hook.Status != usage.HookError {
		t.Errorf("hook = %d/%d characters, status %q; want capped names and error", len(hook.Event), len(hook.Name), hook.Status)
	}
	if hook.ExitCode != 1<<31-1 || hook.DurationMs != 0 {
		t.Errorf("hook ExitCode = %d, DurationMs = %d; want the values clamped", hook.ExitCode, hook.DurationMs)
	}
}
