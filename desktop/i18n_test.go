package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/usage"
)

// errorsCatalogPath is the frontend file that translates bridge codes.
const errorsCatalogPath = "frontend/src/i18n/messages/errors.ts"

// typedStringConsts returns the values of the string constants of typeName
// declared in the non-test files of this package, so that a new constant
// cannot be forgotten in a hand-written list.
func typedStringConsts(t *testing.T, typeName string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				ident, ok := value.Type.(*ast.Ident)
				if !ok || ident.Name != typeName {
					continue
				}
				for i, n := range value.Names {
					lit, ok := value.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Fatalf("%s is not a string literal", n.Name)
					}
					s, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					found[n.Name] = s
				}
			}
		}
	}
	if len(found) == 0 {
		t.Fatalf("no constants of type %s found", typeName)
	}
	return found
}

// catalogKeys returns the keys of the object exported as name in a messages
// file.
func catalogKeys(t *testing.T, source, name string) []string {
	t.Helper()
	loc := regexp.MustCompile(`export const ` + regexp.QuoteMeta(name) + `\b`).FindStringIndex(source)
	if loc == nil {
		t.Fatalf("%s lacks export %s", errorsCatalogPath, name)
	}
	body := source[loc[1]:]
	end := strings.Index(body, "\n}")
	if end < 0 {
		t.Fatalf("export %s is not closed", name)
	}
	key := regexp.MustCompile(`(?m)^\s*'?([A-Za-z0-9_.]+)'?\s*:`)
	var keys []string
	for _, m := range key.FindAllStringSubmatch(body[:end], -1) {
		keys = append(keys, m[1])
	}
	return keys
}

func TestBridgeErrorCodesHaveMessages(t *testing.T) {
	raw, err := os.ReadFile(errorsCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	codeShape := regexp.MustCompile(`^[a-z]+(\.[a-z_]+)+$`)
	codes := typedStringConsts(t, "bridgeError")
	seen := map[string]string{}
	for name, code := range codes {
		if !codeShape.MatchString(code) {
			t.Errorf("%s = %q is not a lower-case dotted code", name, code)
		}
		if other, dup := seen[code]; dup {
			t.Errorf("%s and %s share the code %q", name, other, code)
		}
		seen[code] = name
	}
	for _, locale := range usage.Locales() {
		keys := catalogKeys(t, source, string(locale))
		for _, code := range codes {
			if !slices.Contains(keys, code) {
				t.Errorf("%s: %s lacks %q", errorsCatalogPath, locale, code)
			}
		}
	}
}

func TestDialogCatalogCoversLocales(t *testing.T) {
	verb := regexp.MustCompile(`%\[\d+\][a-z]`)
	keys := typedStringConsts(t, "dialogKey")
	catalog := dialogCatalog()
	if len(catalog) != len(keys) {
		t.Errorf("catalog has %d rows, %d keys are declared", len(catalog), len(keys))
	}
	for name, key := range keys {
		row, ok := catalog[dialogKey(key)]
		if !ok {
			t.Errorf("%s has no row", name)
			continue
		}
		want := verb.FindAllString(row[usage.LocaleDefault], -1)
		slices.Sort(want)
		for _, locale := range usage.Locales() {
			text := row[locale]
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s has no %s text", name, locale)
			}
			got := verb.FindAllString(text, -1)
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Errorf("%s %s verbs = %v, want %v", name, locale, got, want)
			}
		}
	}
}

func TestDialogTextFormatsEveryLocale(t *testing.T) {
	for _, tc := range []struct {
		locale usage.Locale
		want   string
	}{
		{usage.LocaleEnglish, "Extend the collection period from 30 to 60 days?"},
		{usage.LocaleKorean, "수집 기간을 30일에서 60일로 늘릴까요?"},
		{"xx", "Extend the collection period from 30 to 60 days?"},
	} {
		t.Run(string(tc.locale), func(t *testing.T) {
			got := textFor(tc.locale).format(dlgWindowMessage, 30, 60)
			if !strings.HasPrefix(got, tc.want) {
				t.Fatalf("message = %q, want prefix %q", got, tc.want)
			}
		})
	}
	if got := textFor(usage.LocaleKorean).get("missing.key"); got != "missing.key" {
		t.Fatalf("unknown key = %q, want the key itself", got)
	}
}

func TestSetLocaleSwitchesDialogText(t *testing.T) {
	en, ko := textFor(usage.LocaleEnglish), textFor(usage.LocaleKorean)
	for _, tc := range []struct {
		name    string
		codes   []string // SetLocale calls, in order
		wantErr error
		want    dialogText
	}{
		{"default is English", nil, nil, en},
		{"Korean", []string{"ko"}, nil, ko},
		{"back to English", []string{"ko", "en"}, nil, en},
		{"unknown code falls back to English", []string{"ko", "xx"}, errLocaleUnknown, en},
		{"empty code falls back to English", []string{"ko", ""}, errLocaleUnknown, en},
		{"region tag is not a locale", []string{"ko-KR"}, errLocaleUnknown, en},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBridgeHarness(t)
			var err error
			for _, code := range tc.codes {
				err = h.bridge.SetLocale(code)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("SetLocale error = %v, want %v", err, tc.wantErr)
			}
			h.dialog.accept = true
			if _, err = h.bridge.Usage(usage.MethodClearData, "{}"); err != nil {
				t.Fatal(err)
			}
			shown := h.dialog.shown[0]
			wantButtons := []string{tc.want.get(dlgClearAccept), tc.want.get(dlgCancel)}
			if shown.Title != tc.want.get(dlgClearTitle) || shown.Message != tc.want.get(dlgClearMessage) ||
				!slices.Equal(shown.Buttons, wantButtons) || shown.CancelButton != wantButtons[1] || shown.DefaultButton != wantButtons[1] {
				t.Fatalf("dialog = %+v, want %s text", shown, tc.want.locale)
			}
			// The accepted answer is compared with the localized button.
			if calls := h.service.forwarded(); len(calls) != 1 || calls[0].method != usage.MethodClearData {
				t.Fatalf("accepted dialog did not forward: %+v", calls)
			}
			if _, err = h.bridge.EnableUsageWatch(); err != nil {
				t.Fatal(err)
			}
			if watch := h.dialog.shown[1]; watch.Title != tc.want.get(dlgWatchTitle) || watch.Buttons[0] != tc.want.get(dlgWatchAccept) {
				t.Fatalf("consent dialog = %+v, want %s text", watch, tc.want.locale)
			}
		})
	}
}

func TestAsBridgeError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"nil stays nil", nil, nil},
		{"code is kept", errReceiverInvalid, errReceiverInvalid},
		{"wrapped code is kept", fmt.Errorf("pick: %w", errSourcePathInvalid), errSourcePathInvalid},
		{"other failure becomes the fallback", errors.New("open /private/path: denied"), errSourceParentMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := asBridgeError(tc.err, errSourceParentMissing); got != tc.want {
				t.Fatalf("asBridgeError = %v, want %v", got, tc.want)
			}
		})
	}
}

func newEmptyCollector() *collector {
	return &collector{picks: map[string]capability{}, running: map[string]bool{}}
}

func TestBridgeMethodsRejectWithCodes(t *testing.T) {
	far := time.Now().Add(time.Hour)
	for _, tc := range []struct {
		name string
		call func(b *Bridge) error
		want bridgeError
	}{
		{"unknown picker kind", func(b *Bridge) error { _, err := b.Pick("launch"); return err }, errSourceKindUnknown},
		{"history of an unknown source", func(b *Bridge) error { _, err := b.PickHistory("missing"); return err }, errSourceHistoryUnavailable},
		{"observer without a parent", func(b *Bridge) error { _, err := b.PickObserver("missing", "hook", ""); return err }, errSourceParentMissing},
		{"settings of an unknown receiver", func(b *Bridge) error { return b.CopyReceiverSettings("missing") }, errReceiverNotFound},
		{"relative path", func(b *Bridge) error {
			_, err := b.collector.offer(selectedSource{Kind: collect.Transcript, Path: "missing/relative.jsonl"})
			return err
		}, errSourcePathInvalid},
		{"too many open picks", func(b *Bridge) error {
			for i := range 16 {
				b.collector.picks[strconv.Itoa(i)] = capability{expires: far}
			}
			_, err := b.collector.offer(selectedSource{Kind: collect.Telemetry, Path: receiverInstance})
			return err
		}, errSourceTooManyPicks},
		{"unsupported locale", func(b *Bridge) error { return b.SetLocale("xx") }, errLocaleUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &Bridge{ctx: t.Context(), collector: newEmptyCollector()}
			if err := tc.call(b); err != tc.want {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}
