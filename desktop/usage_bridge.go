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
	usagePromptCancel     = "취소"
)

// Errors returned to the WebView. They name no path and repeat no internal
// error text.
var (
	errUsageUnavailable = errors.New("사용 분석을 시작하지 못했습니다")
	errUsageFailed      = errors.New("사용 분석 요청을 처리하지 못했습니다")
	errUsageRequest     = errors.New("요청을 확인해 주세요")
	// The session page recognises the next two sentences by their wording
	// (desktop/frontend/src/usage/views/session/errors.ts); change both sides
	// together.
	errUsageNotFound    = errors.New("세션을 찾을 수 없습니다")
	errUsageNoConsent   = errors.New("사용 기록 수집을 먼저 허용해 주세요")
	errUsageDialog      = errors.New("확인 창을 열 수 없습니다")
	errUsageEndpointOff = errors.New("확장 이벤트 수신기를 먼저 켜 주세요")
	errUsageCredential  = errors.New("수신기 자격을 키체인에 저장할 수 없습니다")
	errUsageClipboard   = errors.New("클립보드에 복사할 수 없습니다")
)

// usagePrompt is one native confirmation. Cancel is always the default
// button, so that a stray key press never widens what the app reads.
type usagePrompt struct {
	kind    wruntime.DialogType
	title   string
	message string
	accept  string
}

func watchPrompt(rootLabel string) usagePrompt {
	return usagePrompt{
		kind:    wruntime.QuestionDialog,
		title:   "사용 기록 수집",
		message: fmt.Sprintf("이 Mac의 %s 아래에 있는 Claude Code 세션 기록을 읽어 사용 현황을 집계할까요? 대화 원문은 저장하지 않습니다.", promptLabel(rootLabel)),
		accept:  "허용",
	}
}

func extensionPrompt() usagePrompt {
	return usagePrompt{
		kind:    wruntime.QuestionDialog,
		title:   "확장 이벤트 수신",
		message: "이 Mac의 다른 도구가 보내는 확장 이벤트를 받을까요? 이 Mac 안에서 보낸 것만 받습니다.",
		accept:  "켜기",
	}
}

func windowPrompt(current, requested int) usagePrompt {
	return usagePrompt{
		kind:    wruntime.QuestionDialog,
		title:   "수집 기간 변경",
		message: fmt.Sprintf("수집 기간을 %d일에서 %d일로 늘릴까요? 더 오래된 세션 기록까지 읽습니다.", current, requested),
		accept:  "늘리기",
	}
}

func includePrompt(project string) usagePrompt {
	return usagePrompt{
		kind:    wruntime.QuestionDialog,
		title:   "프로젝트 다시 수집",
		message: "제외한 프로젝트의 세션 기록을 다시 읽을까요?\n\n" + promptLabel(project),
		accept:  "다시 수집",
	}
}

func clearPrompt() usagePrompt {
	return usagePrompt{
		kind:    wruntime.WarningDialog,
		title:   "사용 분석 자료 지우기",
		message: "모은 사용 분석 자료와 확장 이벤트를 모두 지우고 수집을 끌까요? 지운 자료는 되돌릴 수 없습니다. 원본 세션 기록은 그대로 둡니다.",
		accept:  "지우기",
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
	forward, prompt, err := gateUsageRequest(method, json.RawMessage(params), func() (usage.Status, error) {
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
func gateUsageRequest(method string, params json.RawMessage, current func() (usage.Status, error)) (json.RawMessage, *usagePrompt, error) {
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
			p := extensionPrompt()
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
			p := windowPrompt(status.WindowDays, req.Days)
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
			p := includePrompt(req.Project)
			prompt = &p
		}
		request = req
	case usage.MethodClearData:
		p := clearPrompt()
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

// usageBridgeError maps a service failure to the text shown in the WebView.
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
		Buttons:       []string{prompt.accept, usagePromptCancel},
		DefaultButton: usagePromptCancel,
		CancelButton:  usagePromptCancel,
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
	allowed, err := u.confirm(ctx, watchPrompt(u.rootLabel))
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
