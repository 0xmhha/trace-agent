package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/0xmhha/trace-agent/internal/extension"
	"github.com/0xmhha/trace-agent/internal/usage"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	// usageSenderService names the Keychain item that holds the extension
	// credential. It must not collide with receiverService plus a source ID.
	usageSenderService = "org.0xmhha.trace-agent.usage-events"
	usageLoopbackHost  = "127.0.0.1"
	// usageCallTimeout bounds one service call. Native dialogs are not
	// covered: the user may take as long as they like to answer.
	usageCallTimeout = 30 * time.Second
	// usagePromptLabelRunes keeps a name shown in a native dialog short
	// enough that the question around it stays visible.
	usagePromptLabelRunes = 120
	usagePromptEllipsis   = "…"
)

// usagePrompt is one native confirmation. Cancel is always the default
// button, so that a stray key press never widens what the app reads.
type usagePrompt struct {
	kind    wruntime.DialogType
	title   string
	message string
	accept  string
	cancel  string
}

func watchPrompt(text dialogText, rootLabel string) usagePrompt {
	return usagePrompt{
		kind:    wruntime.QuestionDialog,
		title:   text.get(dlgWatchTitle),
		message: text.format(dlgWatchMessage, promptLabel(rootLabel)),
		accept:  text.get(dlgWatchAccept),
		cancel:  text.get(dlgCancel),
	}
}

func extensionPrompt(text dialogText) usagePrompt {
	return usagePrompt{
		kind:    wruntime.QuestionDialog,
		title:   text.get(dlgExtensionTitle),
		message: text.get(dlgExtensionMsg),
		accept:  text.get(dlgExtensionAccept),
		cancel:  text.get(dlgCancel),
	}
}

func windowPrompt(text dialogText, current, requested int) usagePrompt {
	return usagePrompt{
		kind:    wruntime.QuestionDialog,
		title:   text.get(dlgWindowTitle),
		message: text.format(dlgWindowMessage, current, requested),
		accept:  text.get(dlgWindowAccept),
		cancel:  text.get(dlgCancel),
	}
}

func includePrompt(text dialogText, project string) usagePrompt {
	return usagePrompt{
		kind:    wruntime.QuestionDialog,
		title:   text.get(dlgIncludeTitle),
		message: text.format(dlgIncludeMessage, promptLabel(project)),
		accept:  text.get(dlgIncludeAccept),
		cancel:  text.get(dlgCancel),
	}
}

func clearPrompt(text dialogText) usagePrompt {
	return usagePrompt{
		kind:    wruntime.WarningDialog,
		title:   text.get(dlgClearTitle),
		message: text.get(dlgClearMessage),
		accept:  text.get(dlgClearAccept),
		cancel:  text.get(dlgCancel),
	}
}

// promptLabel prepares a name for a native dialog. The WebView supplies some
// of these names, so line breaks and other non-printing runes are dropped:
// they could push the real question out of sight. A long name keeps its
// tail, which is the part that tells two project paths apart.
func promptLabel(name string) string {
	runes := []rune(strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, name))
	if len(runes) <= usagePromptLabelRunes {
		return string(runes)
	}
	return usagePromptEllipsis + string(runes[len(runes)-usagePromptLabelRunes:])
}

// Usage serves one request of the usage pages. A request that widens what
// the app reads or accepts, or that cannot be undone, is forwarded only
// after a native confirmation; when the user declines, the current status
// is returned unchanged.
func (b *Bridge) Usage(method, params string) (string, error) {
	return b.usage.request(b.ctx, method, params)
}

// EnableUsageWatch asks for consent to read the transcript root in a native
// dialog and starts collection. It reports false when the user declined.
func (b *Bridge) EnableUsageWatch() (bool, error) {
	return b.usage.enableWatch(b.ctx)
}

// DisableUsageWatch withdraws the consent to read transcripts.
func (b *Bridge) DisableUsageWatch() error {
	return b.usage.disableWatch(b.ctx)
}

// CopyExtensionSettings copies the shell settings of the extension endpoint.
// The pasted shell reads the credential from its own Keychain entry at
// execution time; the credential never reaches the WebView or the clipboard.
func (b *Bridge) CopyExtensionSettings() error {
	return b.usage.copyExtensionSettings(b.ctx)
}

// available returns the service, or the error every usage bridge method
// reports while the pipeline is not running.
func (u *usageRuntime) available() (usageService, error) {
	if u == nil || u.service == nil {
		return nil, errUsageUnavailable
	}
	return u.service, nil
}

func (u *usageRuntime) request(ctx context.Context, method, params string) (string, error) {
	svc, err := u.available()
	if err != nil {
		return "", err
	}
	text := textFor(u.locale.get())
	forward, prompt, err := gateUsageRequest(text, method, json.RawMessage(params), func() (usage.Status, error) {
		return currentUsageStatus(ctx, svc)
	})
	if err != nil {
		return "", err
	}
	if prompt != nil {
		allowed, err := u.confirm(ctx, *prompt)
		if err != nil {
			return "", err
		}
		if !allowed {
			method, forward = usage.MethodStatus, nil
		}
	}
	response, err := callUsage(ctx, svc, method, forward)
	if err != nil {
		return "", err
	}
	return string(response), nil
}

// gateUsageRequest decides whether method needs a native confirmation and
// returns the parameters to forward. Gated requests are forwarded in the
// decoded and re-encoded form, so the service acts on exactly the values the
// decision here was based on, however it parses its input.
func gateUsageRequest(text dialogText, method string, params json.RawMessage, current func() (usage.Status, error)) (json.RawMessage, *usagePrompt, error) {
	var (
		request any
		prompt  *usagePrompt
	)
	switch method {
	case usage.MethodSetExtension:
		var req usage.SetEnabled
		if err := decodeUsageParams(params, &req); err != nil {
			return nil, nil, err
		}
		if req.Enabled {
			p := extensionPrompt(text)
			prompt = &p
		}
		request = req
	case usage.MethodSetWindow:
		var req usage.SetWindow
		if err := decodeUsageParams(params, &req); err != nil {
			return nil, nil, err
		}
		if req.Days > usage.MaxWindowDays {
			return nil, nil, errUsageRequest
		}
		status, err := current()
		if err != nil {
			return nil, nil, err
		}
		if req.Days > status.WindowDays {
			p := windowPrompt(text, status.WindowDays, req.Days)
			prompt = &p
		}
		request = req
	case usage.MethodSetExcluded:
		var req usage.SetExcluded
		if err := decodeUsageParams(params, &req); err != nil {
			return nil, nil, err
		}
		if !req.Excluded {
			if promptLabel(req.Project) == "" {
				return nil, nil, errUsageRequest
			}
			p := includePrompt(text, req.Project)
			prompt = &p
		}
		request = req
	case usage.MethodClearData:
		p := clearPrompt(text)
		return params, &p, nil
	default:
		return params, nil, nil
	}
	forward, err := json.Marshal(request)
	if err != nil {
		return nil, nil, errUsageRequest
	}
	return forward, prompt, nil
}

func decodeUsageParams(params json.RawMessage, into any) error {
	if err := json.Unmarshal(params, into); err != nil {
		return errUsageRequest
	}
	return nil
}

func currentUsageStatus(ctx context.Context, svc usageService) (usage.Status, error) {
	raw, err := callUsage(ctx, svc, usage.MethodStatus, nil)
	if err != nil {
		return usage.Status{}, err
	}
	var status usage.Status
	if err = json.Unmarshal(raw, &status); err != nil {
		return usage.Status{}, errUsageFailed
	}
	return status, nil
}

// callUsage forwards one request and translates its failure for the WebView.
func callUsage(ctx context.Context, svc usageService, method string, params json.RawMessage) (json.RawMessage, error) {
	callCtx, cancel := context.WithTimeout(ctx, usageCallTimeout)
	defer cancel()
	response, err := svc.Handle(callCtx, method, params)
	if err != nil {
		return nil, usageBridgeError(err)
	}
	return response, nil
}

// usageBridgeError maps a service failure to the code sent to the WebView.
func usageBridgeError(err error) error {
	switch {
	case errors.Is(err, usage.ErrBadRequest):
		return errUsageRequest
	case errors.Is(err, usage.ErrNotFound):
		return errUsageNotFound
	case errors.Is(err, usage.ErrNoConsent):
		return errUsageNoConsent
	default:
		return errUsageFailed
	}
}

// confirm shows prompt and reports whether the user accepted it.
func (u *usageRuntime) confirm(ctx context.Context, prompt usagePrompt) (bool, error) {
	answer, err := u.dialog(ctx, wruntime.MessageDialogOptions{
		Type:          prompt.kind,
		Title:         prompt.title,
		Message:       prompt.message,
		Buttons:       []string{prompt.accept, prompt.cancel},
		DefaultButton: prompt.cancel,
		CancelButton:  prompt.cancel,
	})
	if err != nil {
		return false, errUsageDialog
	}
	return answer == prompt.accept, nil
}

func (u *usageRuntime) enableWatch(ctx context.Context) (bool, error) {
	svc, err := u.available()
	if err != nil {
		return false, err
	}
	allowed, err := u.confirm(ctx, watchPrompt(textFor(u.locale.get()), u.rootLabel))
	if err != nil || !allowed {
		return false, err
	}
	if err = setUsageConsent(ctx, svc, true); err != nil {
		return false, err
	}
	return true, nil
}

func (u *usageRuntime) disableWatch(ctx context.Context) error {
	svc, err := u.available()
	if err != nil {
		return err
	}
	return setUsageConsent(ctx, svc, false)
}

func setUsageConsent(ctx context.Context, svc usageService, granted bool) error {
	callCtx, cancel := context.WithTimeout(ctx, usageCallTimeout)
	defer cancel()
	if err := svc.SetConsent(callCtx, granted); err != nil {
		return usageBridgeError(err)
	}
	return nil
}

func (u *usageRuntime) copyExtensionSettings(ctx context.Context) error {
	if _, err := u.available(); err != nil {
		return err
	}
	port, bearer, ok := u.extension.credential()
	if !ok {
		return errUsageEndpointOff
	}
	if err := u.saveCredential(usageSenderService, bearer); err != nil {
		return errUsageCredential
	}
	if err := u.clipboard(ctx, extensionSenderSettings(port)); err != nil {
		return errUsageClipboard
	}
	return nil
}

// extensionSenderSettings returns the shell lines a sender needs. The port is
// the only variable part, so nothing a sender or the WebView controls can
// reach the pasted shell.
func extensionSenderSettings(port int) string {
	return fmt.Sprintf("export TRACE_EVENTS_URL='http://%s:%d%s'\nexport TRACE_EVENTS_TOKEN=\"$(/usr/bin/security find-generic-password -a sender -s '%s' -w)\"",
		usageLoopbackHost, port, extension.EventsPath, usageSenderService)
}
