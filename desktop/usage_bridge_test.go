package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/usage"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// fakeDialog answers every native confirmation with its accept button or,
// like a dismissed dialog, with the cancel button.
type fakeDialog struct {
	accept bool
	err    error
	shown  []wruntime.MessageDialogOptions
}

func (d *fakeDialog) show(_ context.Context, options wruntime.MessageDialogOptions) (string, error) {
	d.shown = append(d.shown, options)
	if d.err != nil {
		return "", d.err
	}
	if d.accept {
		return options.Buttons[0], nil
	}
	return options.CancelButton, nil
}

type fakeClipboard struct {
	err  error
	text []string
}

func (c *fakeClipboard) set(_ context.Context, text string) error {
	if c.err != nil {
		return c.err
	}
	c.text = append(c.text, text)
	return nil
}

type savedCredential struct{ service, credential string }

type fakeKeychain struct {
	err   error
	saved []savedCredential
}

func (k *fakeKeychain) save(service, credential string) error {
	if k.err != nil {
		return k.err
	}
	k.saved = append(k.saved, savedCredential{service, credential})
	return nil
}

// bridgeHarness is a bridge over a running fake pipeline.
type bridgeHarness struct {
	bridge    *Bridge
	service   *fakeUsageService
	listener  *fakeUsageListener
	dialog    *fakeDialog
	clipboard *fakeClipboard
	keychain  *fakeKeychain
}

func newBridgeHarness(t *testing.T) *bridgeHarness {
	t.Helper()
	h := &bridgeHarness{service: newFakeUsageService(), listener: newFakeUsageListener(), dialog: &fakeDialog{}, clipboard: &fakeClipboard{}, keychain: &fakeKeychain{}}
	u := &usageRuntime{
		service:        h.service,
		extension:      newExtensionControl(nil, h.listener.listen, fixedKey(testInstallationKey(), nil), 0),
		rootLabel:      testRootLabel,
		dialog:         h.dialog.show,
		clipboard:      h.clipboard.set,
		saveCredential: h.keychain.save,
	}
	h.bridge = &Bridge{ctx: t.Context(), usage: u}
	u.locale = &h.bridge.locale
	return h
}

func statusJSON(t *testing.T, s usage.Status) string {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func handledJSON(method string) string { return fmt.Sprintf(`{"handled":%q}`, method) }

func TestUsageConfirmationRouting(t *testing.T) {
	const widened = usage.DefaultWindowDays + 30
	for _, tc := range []struct {
		name       string
		method     string
		params     string
		accept     bool
		wantDialog string // a fragment of the dialog message, empty for no dialog
		wantCall   *usageCall
		wantStatus bool
	}{
		{"status is not confirmed", usage.MethodStatus, "", false, "", nil, true},
		{"read request passes through unchanged", usage.MethodOverview, `{"range":{"from":"x"},"extra":1}`, false, "",
			&usageCall{usage.MethodOverview, `{"range":{"from":"x"},"extra":1}`}, false},
		{"rescan is not confirmed", usage.MethodRescan, "{}", false, "", &usageCall{usage.MethodRescan, "{}"}, false},
		{"unknown method is left to the service", "launch", "{}", false, "", &usageCall{"launch", "{}"}, false},

		{"enabling the endpoint, accepted", usage.MethodSetExtension, `{"enabled":true}`, true, "extension events",
			&usageCall{usage.MethodSetExtension, `{"enabled":true}`}, false},
		{"enabling the endpoint, declined", usage.MethodSetExtension, `{"enabled":true}`, false, "extension events", nil, true},
		{"disabling the endpoint is not confirmed", usage.MethodSetExtension, `{"enabled":false}`, false, "",
			&usageCall{usage.MethodSetExtension, `{"enabled":false}`}, false},
		{"field name in another case is still confirmed", usage.MethodSetExtension, `{"ENABLED":true}`, false, "extension events", nil, true},
		{"repeated field is confirmed by its effective value", usage.MethodSetExtension, `{"enabled":false,"enabled":true}`, true, "extension events",
			&usageCall{usage.MethodSetExtension, `{"enabled":true}`}, false},

		{"widening the window, accepted", usage.MethodSetWindow, fmt.Sprintf(`{"days":%d}`, widened), true, "from 30 to 60 days",
			&usageCall{usage.MethodSetWindow, `{"days":60}`}, false},
		{"widening the window, declined", usage.MethodSetWindow, fmt.Sprintf(`{"days":%d}`, widened), false, "from 30 to 60 days", nil, true},
		{"same window is not confirmed", usage.MethodSetWindow, `{"days":30}`, false, "", &usageCall{usage.MethodSetWindow, `{"days":30}`}, false},
		{"narrowing the window is not confirmed", usage.MethodSetWindow, `{"days":7}`, false, "", &usageCall{usage.MethodSetWindow, `{"days":7}`}, false},
		{"repeated days are confirmed by the effective value", usage.MethodSetWindow, `{"days":7,"days":90}`, false, "from 30 to 90 days", nil, true},
		{"widest window, accepted", usage.MethodSetWindow, fmt.Sprintf(`{"days":%d}`, usage.MaxWindowDays), true, "to 365 days",
			&usageCall{usage.MethodSetWindow, `{"days":365}`}, false},

		{"including a project again, accepted", usage.MethodSetExcluded, `{"project":"-synthetic-project","excluded":false}`, true, "-synthetic-project",
			&usageCall{usage.MethodSetExcluded, `{"project":"-synthetic-project","excluded":false}`}, false},
		{"including a project again, declined", usage.MethodSetExcluded, `{"project":"-synthetic-project","excluded":false}`, false, "-synthetic-project", nil, true},
		{"missing excluded field means include", usage.MethodSetExcluded, `{"project":"-synthetic-project"}`, false, "-synthetic-project", nil, true},
		{"excluding a project is not confirmed", usage.MethodSetExcluded, `{"project":"-synthetic-project","excluded":true}`, false, "",
			&usageCall{usage.MethodSetExcluded, `{"project":"-synthetic-project","excluded":true}`}, false},

		{"clearing data, accepted", usage.MethodClearData, "{}", true, "cannot be restored", &usageCall{usage.MethodClearData, "{}"}, false},
		{"clearing data, declined", usage.MethodClearData, "{}", false, "cannot be restored", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBridgeHarness(t)
			h.dialog.accept = tc.accept
			got, err := h.bridge.Usage(tc.method, tc.params)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantDialog == "" && len(h.dialog.shown) != 0 {
				t.Fatalf("unexpected dialog %q", h.dialog.shown[0].Message)
			}
			if tc.wantDialog != "" {
				if len(h.dialog.shown) != 1 {
					t.Fatalf("dialogs shown = %d, want 1", len(h.dialog.shown))
				}
				shown := h.dialog.shown[0]
				if !strings.Contains(shown.Message, tc.wantDialog) {
					t.Fatalf("dialog message %q lacks %q", shown.Message, tc.wantDialog)
				}
				if len(shown.Buttons) != 2 || shown.Buttons[1] != usagePromptCancel || shown.DefaultButton != usagePromptCancel ||
					shown.CancelButton != usagePromptCancel || shown.Buttons[0] == usagePromptCancel || shown.Title == "" {
					t.Fatalf("dialog does not default to cancel: %+v", shown)
				}
			}
			forwarded := h.service.forwarded()
			if tc.wantCall == nil && len(forwarded) != 0 {
				t.Fatalf("request reached the service: %+v", forwarded)
			}
			if tc.wantCall != nil && !slices.Equal(forwarded, []usageCall{*tc.wantCall}) {
				t.Fatalf("forwarded = %+v, want %+v", forwarded, *tc.wantCall)
			}
			want := handledJSON(tc.method)
			if tc.wantStatus {
				want = statusJSON(t, h.service.status)
			}
			if got != want {
				t.Fatalf("response = %s, want %s", got, want)
			}
		})
	}
}

func TestUsageRequestRefusals(t *testing.T) {
	for _, tc := range []struct {
		name       string
		method     string
		params     string
		arrange    func(*bridgeHarness)
		want       error
		wantDialog int
	}{
		{"malformed endpoint request", usage.MethodSetExtension, `{"enabled":"yes"}`, nil, errUsageRequest, 0},
		{"empty endpoint request", usage.MethodSetExtension, "", nil, errUsageRequest, 0},
		{"trailing data", usage.MethodSetWindow, `{"days":7}{"days":300}`, nil, errUsageRequest, 0},
		{"window beyond the limit", usage.MethodSetWindow, fmt.Sprintf(`{"days":%d}`, usage.MaxWindowDays+1), nil, errUsageRequest, 0},
		{"malformed window request", usage.MethodSetWindow, `[30]`, nil, errUsageRequest, 0},
		{"including a project without a name", usage.MethodSetExcluded, `{"project":"","excluded":false}`, nil, errUsageRequest, 0},
		{"including a project with an invisible name", usage.MethodSetExcluded, `{"project":"\n\u0000","excluded":false}`, nil, errUsageRequest, 0},
		{"malformed exclusion request", usage.MethodSetExcluded, `{"project":7}`, nil, errUsageRequest, 0},
		{"status unreadable before widening", usage.MethodSetWindow, `{"days":90}`, func(h *bridgeHarness) {
			h.service.handleErr[usage.MethodStatus] = errTest
		}, errUsageFailed, 0},
		{"dialog cannot open", usage.MethodClearData, "{}", func(h *bridgeHarness) { h.dialog.err = errTest }, errUsageDialog, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBridgeHarness(t)
			h.dialog.accept = true
			if tc.arrange != nil {
				tc.arrange(h)
			}
			got, err := h.bridge.Usage(tc.method, tc.params)
			if !errors.Is(err, tc.want) || got != "" {
				t.Fatalf("response %q error %v, want %v", got, err, tc.want)
			}
			if len(h.dialog.shown) != tc.wantDialog {
				t.Fatalf("dialogs shown = %d, want %d", len(h.dialog.shown), tc.wantDialog)
			}
			if forwarded := h.service.forwarded(); len(forwarded) != 0 {
				t.Fatalf("refused request reached the service: %+v", forwarded)
			}
		})
	}
}

func TestUsageErrorsAreGeneric(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"bad request", fmt.Errorf("decode /private/path: %w", usage.ErrBadRequest), errUsageRequest},
		{"unknown session", fmt.Errorf("session s1: %w", usage.ErrNotFound), errUsageNotFound},
		{"no consent", usage.ErrNoConsent, errUsageNoConsent},
		{"deadline", context.DeadlineExceeded, errUsageFailed},
		{"internal failure", errTest, errUsageFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBridgeHarness(t)
			h.service.handleErr[usage.MethodSession] = tc.err
			_, err := h.bridge.Usage(usage.MethodSession, `{"sessionId":"s1"}`)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if text := err.Error(); strings.Contains(text, "private") || strings.Contains(text, "usage:") || strings.Contains(text, "s1") {
				t.Fatalf("error text leaks internals: %q", text)
			}
		})
	}
}

func TestUsageWatchConsent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		accept      bool
		dialogErr   error
		consentErr  error
		want        bool
		wantErr     error
		wantConsent []bool
	}{
		{"accepted", true, nil, nil, true, nil, []bool{true}},
		{"declined", false, nil, nil, false, nil, nil},
		{"dialog cannot open", true, errTest, nil, false, errUsageDialog, nil},
		{"service refuses", true, nil, errTest, false, errUsageFailed, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBridgeHarness(t)
			h.dialog.accept, h.dialog.err = tc.accept, tc.dialogErr
			h.service.consentErr = tc.consentErr
			got, err := h.bridge.EnableUsageWatch()
			if got != tc.want || !errors.Is(err, tc.wantErr) {
				t.Fatalf("EnableUsageWatch = %t, %v; want %t, %v", got, err, tc.want, tc.wantErr)
			}
			if len(h.dialog.shown) != 1 {
				t.Fatalf("dialogs shown = %d, want 1", len(h.dialog.shown))
			}
			shown := h.dialog.shown[0]
			if !strings.Contains(shown.Message, testRootLabel) || shown.DefaultButton != usagePromptCancel || shown.CancelButton != usagePromptCancel {
				t.Fatalf("consent dialog = %+v", shown)
			}
			if got := h.service.consents(); !slices.Equal(got, tc.wantConsent) {
				t.Fatalf("consent calls = %v, want %v", got, tc.wantConsent)
			}
		})
	}
}

func TestDisableUsageWatch(t *testing.T) {
	h := newBridgeHarness(t)
	if err := h.bridge.DisableUsageWatch(); err != nil {
		t.Fatal(err)
	}
	if got := h.service.consents(); !slices.Equal(got, []bool{false}) || len(h.dialog.shown) != 0 {
		t.Fatalf("consent calls = %v, dialogs = %d", got, len(h.dialog.shown))
	}
	h.service.consentErr = errTest
	if err := h.bridge.DisableUsageWatch(); !errors.Is(err, errUsageFailed) || strings.Contains(err.Error(), "private") {
		t.Fatalf("error = %v", err)
	}
}

func TestCopyExtensionSettings(t *testing.T) {
	token, err := usageEventsToken(testInstallationKey())
	if err != nil {
		t.Fatal(err)
	}
	wantText := "export TRACE_EVENTS_URL='http://127.0.0.1:41000/v1/events'\n" +
		"export TRACE_EVENTS_TOKEN=\"$(/usr/bin/security find-generic-password -a sender -s '" + usageSenderService + "' -w)\""
	for _, tc := range []struct {
		name        string
		running     bool
		keychainErr error
		clipErr     error
		wantErr     error
		wantSaved   []savedCredential
		wantText    []string
	}{
		{"endpoint not running", false, nil, nil, errUsageEndpointOff, nil, nil},
		{"copied", true, nil, nil, nil, []savedCredential{{usageSenderService, token}}, []string{wantText}},
		{"keychain refuses", true, errTest, nil, errUsageCredential, nil, nil},
		{"clipboard refuses", true, nil, errTest, errUsageClipboard, []savedCredential{{usageSenderService, token}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBridgeHarness(t)
			h.keychain.err, h.clipboard.err = tc.keychainErr, tc.clipErr
			if tc.running {
				if _, err := h.bridge.usage.extension.SetEnabled(t.Context(), true); err != nil {
					t.Fatal(err)
				}
			}
			err := h.bridge.CopyExtensionSettings()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatalf("error text leaks internals: %q", err)
			}
			if !slices.Equal(h.keychain.saved, tc.wantSaved) {
				t.Fatal("keychain content differs from the expected credential")
			}
			if !slices.Equal(h.clipboard.text, tc.wantText) {
				t.Fatalf("clipboard = %q, want %q", h.clipboard.text, tc.wantText)
			}
			for _, text := range h.clipboard.text {
				if strings.Contains(text, token) {
					t.Fatal("clipboard carries the credential")
				}
			}
		})
	}
}

func TestCopyExtensionSettingsAfterStop(t *testing.T) {
	h := newBridgeHarness(t)
	control := h.bridge.usage.extension
	if _, err := control.SetEnabled(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := control.SetEnabled(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := h.bridge.CopyExtensionSettings(); !errors.Is(err, errUsageEndpointOff) || len(h.keychain.saved) != 0 {
		t.Fatalf("settings copied for a stopped endpoint: %v", err)
	}
}

func TestUsageSenderServiceIsDistinct(t *testing.T) {
	if strings.HasPrefix(usageSenderService, receiverService) || usageSenderService == strings.TrimSuffix(receiverService, ".") {
		t.Fatalf("%q collides with the trace receiver items %q", usageSenderService, receiverService)
	}
}

func TestPromptLabel(t *testing.T) {
	long := strings.Repeat("a", usagePromptLabelRunes) + "-tail"
	for _, tc := range []struct{ name, in, want string }{
		{"plain name", "-synthetic-project", "-synthetic-project"},
		{"line breaks removed", "project\n\n\n허용하지 않아도 됩니다\r\t", "project허용하지 않아도 됩니다"},
		{"control and format runes removed", "a\u0000b\u202ec\u200bd", "abcd"},
		{"limit is not cut", strings.Repeat("가", usagePromptLabelRunes), strings.Repeat("가", usagePromptLabelRunes)},
		{"long name keeps its tail", long, usagePromptEllipsis + long[len("-tail"):]},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := promptLabel(tc.in)
			if got != tc.want || !utf8.ValidString(got) {
				t.Fatalf("promptLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIncludePromptKeepsQuestionVisible(t *testing.T) {
	h := newBridgeHarness(t)
	params, err := json.Marshal(usage.SetExcluded{Project: "p\n\n\n\n\n\n\n\n" + strings.Repeat("x", 4*usagePromptLabelRunes)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.bridge.Usage(usage.MethodSetExcluded, string(params)); err != nil {
		t.Fatal(err)
	}
	message := h.dialog.shown[0].Message
	if strings.Count(message, "\n") != 2 || utf8.RuneCountInString(message) > 2*usagePromptLabelRunes {
		t.Fatalf("project name reshaped the dialog: %q", message)
	}
}
