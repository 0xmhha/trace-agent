package timeline

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// testTail is the itemless tail bound of the bounded-read tests.
const testTail = 3

// itemless is a line the timeline reads without showing it.
var itemless = obj{"type": "mode", "mode": "default"}

// boundedPages follows NextCursor through readBounded and returns every page.
func boundedPages(t *testing.T, data []byte, limit int) []usage.TimelinePage {
	t.Helper()
	req := usage.TimelineRequest{Limit: limit}
	var pages []usage.TimelinePage
	for len(pages) < maxTestPages {
		page, err := readBounded(bytes.NewReader(data), req, usage.MaxLineBytes, testTail)
		if err != nil {
			t.Fatalf("readBounded(%+v): %v", req, err)
		}
		pages = append(pages, page)
		if page.NextCursor == "" {
			return pages
		}
		req.Cursor = page.NextCursor
	}
	t.Fatalf("paging did not end after %d pages", maxTestPages)
	return nil
}

func repeatLine(line any, n int) []any {
	lines := make([]any, n)
	for i := range lines {
		lines[i] = line
	}
	return lines
}

func TestReadBoundsItemlessTail(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	first, second := promptLine("p1", at, "first"), promptLine("p2", at, "second")
	broken := `{"type":`
	for _, tc := range []struct {
		name string
		data []byte
		// wantPages is the number of pages; wantItems the items of each.
		wantItems []int
		wantSkip  int
	}{
		{
			name:      "tail shorter than the bound reaches the next item",
			data:      transcript(t, append(append([]any{first}, repeatLine(itemless, testTail-1)...), second)...),
			wantItems: []int{1, 1},
		},
		{
			name:      "tail as long as the bound ends the page before the next item",
			data:      transcript(t, append(append([]any{first}, repeatLine(itemless, testTail)...), second)...),
			wantItems: []int{1, 1},
		},
		{
			name:      "tail longer than the bound ends the page inside the tail",
			data:      transcript(t, append(append([]any{first}, repeatLine(broken, testTail+2)...), second)...),
			wantItems: []int{1, 1},
			wantSkip:  testTail + 2,
		},
		{
			name:      "tail up to the end of the transcript ends the stream",
			data:      transcript(t, append([]any{first}, repeatLine(itemless, testTail-1)...)...),
			wantItems: []int{1},
		},
		{
			name:      "tail that reaches the bound at the end leaves an empty last page",
			data:      transcript(t, append([]any{first}, repeatLine(itemless, testTail+1)...)...),
			wantItems: []int{1, 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pages := boundedPages(t, tc.data, 1)
			var items []usage.TimelineItem
			skipped := 0
			gotItems := make([]int, 0, len(pages))
			for _, page := range pages {
				gotItems = append(gotItems, len(page.Items))
				items = append(items, page.Items...)
				skipped += page.Skipped
			}
			if !reflect.DeepEqual(gotItems, tc.wantItems) {
				t.Errorf("items per page = %v; want %v", gotItems, tc.wantItems)
			}
			if skipped != tc.wantSkip {
				t.Errorf("Skipped over all pages = %d; want %d", skipped, tc.wantSkip)
			}
			full, _, fullSkipped := readAll(t, tc.data, usage.TimelineRequest{Limit: usage.MaxPageSize})
			if !reflect.DeepEqual(items, full) || skipped != fullSkipped {
				t.Errorf("bounded pages differ from the full read:\n got %+v\nwant %+v", rowsOf(items), rowsOf(full))
			}
		})
	}
}

// The page stops in the tail only where a line ends: an unterminated last
// line cannot carry a cursor, so the stream ends there.
func TestReadBoundedTailUnterminatedLastLine(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	data := transcript(t, append([]any{promptLine("p1", at, "first")}, repeatLine(itemless, testTail)...)...)
	data = bytes.TrimSuffix(data, []byte("\n"))
	pages := boundedPages(t, data, 1)
	if len(pages) != 1 || len(pages[0].Items) != 1 || pages[0].NextCursor != "" {
		t.Fatalf("pages = %+v; want one page with one item and no cursor", pages)
	}
}

// The default bound keeps the real reader paging the same way.
func TestReadDefaultTailBound(t *testing.T) {
	const at = "2026-03-01T09:00:00Z"
	lines := append([]any{promptLine("p1", at, "first")}, repeatLine(itemless, maxTailLines)...)
	lines = append(lines, promptLine("p2", at, "second"))
	data := transcript(t, lines...)
	page := mustRead(t, data, usage.TimelineRequest{Limit: 1})
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("first page = %d items, cursor %q; want 1 item and a cursor", len(page.Items), page.NextCursor)
	}
	items, _, _ := readAll(t, data, usage.TimelineRequest{Limit: 1})
	if len(items) != 2 {
		t.Fatalf("items over all pages = %d; want 2", len(items))
	}
}

// A secret split by the masking cut must not reach the screen, even when
// masking the text before it frees enough room to bring the cut into view.
func TestDisplayTextHidesSecretSplitByTheCut(t *testing.T) {
	const visiblePrefix = 10 // shorter than the 16 characters the pattern needs
	masked := "gh" + "p_" + strings.Repeat("A", 40) + " "
	tokenHead := "s" + "k-"
	splitChar := "q"
	split := tokenHead + strings.Repeat(splitChar, 30)

	target := usage.MaxMaskInput - len(tokenHead) - visiblePrefix
	var b strings.Builder
	for b.Len()+len(masked) <= target {
		b.WriteString(masked)
	}
	b.WriteString(strings.Repeat(" ", target-b.Len()))
	b.WriteString(split + " tail")
	text := b.String()
	if cut := text[:usage.MaxMaskInput]; !strings.HasSuffix(cut, tokenHead+strings.Repeat(splitChar, visiblePrefix)) {
		t.Fatalf("test text is not split at the cut: %q", cut[len(cut)-20:])
	}

	got, truncated, count := displayText(text)
	if !truncated {
		t.Error("Truncated = false; want true")
	}
	if count == 0 {
		t.Error("nothing was masked")
	}
	if strings.Contains(got, splitChar) || strings.Contains(got, tokenHead) {
		t.Errorf("output holds a fragment of the split secret: ...%q", got[max(len(got)-40, 0):])
	}
	if strings.Contains(got, "gh"+"p_") {
		t.Error("output holds an unmasked token")
	}
}

func TestCommonPrefix(t *testing.T) {
	for _, tc := range []struct{ a, b, want string }{
		{"", "", ""},
		{"abc", "abd", "ab"},
		{"abc", "ab", "ab"},
		{"ab", "abc", "ab"},
		// Two runes that share their first byte must not leave half a rune.
		{"x가", "x각", "x"},
	} {
		got := commonPrefix(tc.a, tc.b)
		if got != tc.want || !utf8.ValidString(got) {
			t.Errorf("commonPrefix(%q, %q) = %q; want %q", tc.a, tc.b, got, tc.want)
		}
	}
}
