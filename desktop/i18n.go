package main

import (
	"errors"
	"fmt"
	"sync"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// bridgeError is an error a Bridge method returns to the WebView. Its text
// is a stable code that the page translates from the errors namespace
// (desktop/frontend/src/i18n/messages/errors.ts), so the wording lives in one
// place per language and the page can branch on the code. Every constant of
// this type must have an entry in that file; a unit test checks it.
type bridgeError string

func (e bridgeError) Error() string { return string(e) }

// Codes of failures that reach the WebView. They name no path and repeat no
// internal error text.
const (
	errBootstrapFailed bridgeError = "bootstrap.failed"
	errLocaleUnknown   bridgeError = "locale.unsupported"

	errSourceKindUnknown        bridgeError = "source.kind_unknown"
	errSourcePickerFailed       bridgeError = "source.picker_failed"
	errSourcePathInvalid        bridgeError = "source.path_invalid"
	errSourceTooManyPicks       bridgeError = "source.too_many_picks"
	errSourceHistoryUnavailable bridgeError = "source.history_unavailable"
	errSourceParentMissing      bridgeError = "source.parent_missing"

	errReceiverInvalid    bridgeError = "receiver.invalid"
	errReceiverDialog     bridgeError = "receiver.dialog"
	errReceiverNotFound   bridgeError = "receiver.not_found"
	errReceiverSourceOff  bridgeError = "receiver.source_disabled"
	errReceiverCredential bridgeError = "receiver.credential"
	errReceiverClipboard  bridgeError = "receiver.clipboard"
	errUsageUnavailable   bridgeError = "usage.unavailable"
	errUsageFailed        bridgeError = "usage.failed"
	errUsageRequest       bridgeError = "usage.bad_request"
	errUsageNotFound      bridgeError = "usage.not_found"
	errUsageNoConsent     bridgeError = "usage.no_consent"
	errUsageDialog        bridgeError = "usage.dialog"
	errUsageEndpointOff   bridgeError = "usage.endpoint_off"
	errUsageCredential    bridgeError = "usage.credential"
	errUsageClipboard     bridgeError = "usage.clipboard"
)

// asBridgeError keeps a code that is already meant for the WebView and
// replaces any other failure with fallback, so that internal error text
// never reaches the page.
func asBridgeError(err error, fallback bridgeError) error {
	if err == nil {
		return nil
	}
	var code bridgeError
	if errors.As(err, &code) {
		return code
	}
	return fallback
}

// localeHolder is the language the WebView last asked for. Native dialogs
// are worded in it. The zero value means the default locale.
type localeHolder struct {
	mu      sync.RWMutex
	current usage.Locale
}

// set stores the normalized form of code and reports whether code was
// supported.
func (h *localeHolder) set(code string) bool {
	l := usage.NormalizeLocale(usage.Locale(code))
	h.mu.Lock()
	h.current = l
	h.mu.Unlock()
	return string(l) == code
}

// get returns the current locale. A nil holder yields the default locale.
func (h *localeHolder) get() usage.Locale {
	if h == nil {
		return usage.LocaleDefault
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return usage.NormalizeLocale(h.current)
}

// SetLocale sets the language of native dialogs. The page calls it at start
// and whenever the user changes the language. An unsupported code selects
// the default language and is reported as locale.unsupported.
func (b *Bridge) SetLocale(code string) error {
	if !b.locale.set(code) {
		return errLocaleUnknown
	}
	return nil
}

// usagePromptCancel is the English cancel label. Tests that drive the
// usage pages in a browser answer dialogs with it.
const usagePromptCancel = "Cancel"

// dialogKey names one text of a native dialog.
type dialogKey string

const (
	dlgCancel  dialogKey = "cancel"
	dlgConnect dialogKey = "connect"

	dlgReceiverTitle   dialogKey = "receiver.title"
	dlgReceiverOTel    dialogKey = "receiver.otel"     // %[1]s: client name
	dlgReceiverOwner   dialogKey = "receiver.observer" // %[1]s: receiver label
	dlgReceiverHook    dialogKey = "receiver.hook"
	dlgReceiverMCP     dialogKey = "receiver.mcp" // %[1]s: server name
	dlgPickDirectory   dialogKey = "pick.directory"
	dlgPickFile        dialogKey = "pick.file"
	dlgPickFileFilter  dialogKey = "pick.filter"
	dlgWatchTitle      dialogKey = "watch.title"
	dlgWatchMessage    dialogKey = "watch.message" // %[1]s: transcript root
	dlgWatchAccept     dialogKey = "watch.accept"
	dlgExtensionTitle  dialogKey = "extension.title"
	dlgExtensionMsg    dialogKey = "extension.message"
	dlgExtensionAccept dialogKey = "extension.accept"
	dlgWindowTitle     dialogKey = "window.title"
	dlgWindowMessage   dialogKey = "window.message" // %[1]d: current days, %[2]d: requested days
	dlgWindowAccept    dialogKey = "window.accept"
	dlgIncludeTitle    dialogKey = "include.title"
	dlgIncludeMessage  dialogKey = "include.message" // %[1]s: project
	dlgIncludeAccept   dialogKey = "include.accept"
	dlgClearTitle      dialogKey = "clear.title"
	dlgClearMessage    dialogKey = "clear.message"
	dlgClearAccept     dialogKey = "clear.accept"
)

// dialogCatalog is every native dialog text by key and locale. Adding a
// locale means one more entry in each row; a unit test checks that every
// row covers usage.Locales().
var dialogCatalog = sync.OnceValue(func() map[dialogKey]map[usage.Locale]string {
	en, ko := usage.LocaleEnglish, usage.LocaleKorean
	return map[dialogKey]map[usage.Locale]string{
		dlgCancel:  {en: usagePromptCancel, ko: "취소"},
		dlgConnect: {en: "Connect", ko: "연결"},

		dlgReceiverTitle: {en: "Connect local receiver", ko: "로컬 수신기 연결"},
		dlgReceiverOTel:  {en: "Receive the OTel records that %[1]s sends on this Mac?", ko: "이 Mac에서 %[1]s가 보낸 OTel 기록을 받을까요?"},
		dlgReceiverOwner: {en: "Connect the records of %[1]s to the selected client?", ko: "%[1]s의 기록을 선택한 클라이언트와 연결할까요?"},
		dlgReceiverHook:  {en: "Claude Code tool hook", ko: "Claude Code 도구 훅"},
		dlgReceiverMCP:   {en: "MCP server %[1]s", ko: "MCP 서버 %[1]s"},

		dlgPickDirectory:  {en: "Choose a directory to collect", ko: "수집할 디렉터리 선택"},
		dlgPickFile:       {en: "Choose a session file to collect", ko: "수집할 세션 파일 선택"},
		dlgPickFileFilter: {en: "Session records", ko: "세션 기록"},

		dlgWatchTitle: {en: "Collect usage records", ko: "사용 기록 수집"},
		dlgWatchMessage: {
			en: "Read the Claude Code session records under %[1]s on this Mac to summarize usage? Conversation text is not stored.",
			ko: "이 Mac의 %[1]s 아래에 있는 Claude Code 세션 기록을 읽어 사용 현황을 집계할까요? 대화 원문은 저장하지 않습니다.",
		},
		dlgWatchAccept: {en: "Allow", ko: "허용"},

		dlgExtensionTitle: {en: "Receive extension events", ko: "확장 이벤트 수신"},
		dlgExtensionMsg: {
			en: "Receive extension events that other tools on this Mac send? Only events sent from this Mac are accepted.",
			ko: "이 Mac의 다른 도구가 보내는 확장 이벤트를 받을까요? 이 Mac 안에서 보낸 것만 받습니다.",
		},
		dlgExtensionAccept: {en: "Turn on", ko: "켜기"},

		dlgWindowTitle: {en: "Change collection period", ko: "수집 기간 변경"},
		dlgWindowMessage: {
			en: "Extend the collection period from %[1]d to %[2]d days? Older session records will be read as well.",
			ko: "수집 기간을 %[1]d일에서 %[2]d일로 늘릴까요? 더 오래된 세션 기록까지 읽습니다.",
		},
		dlgWindowAccept: {en: "Extend", ko: "늘리기"},

		dlgIncludeTitle: {en: "Collect project again", ko: "프로젝트 다시 수집"},
		dlgIncludeMessage: {
			en: "Read the session records of this excluded project again?\n\n%[1]s",
			ko: "제외한 프로젝트의 세션 기록을 다시 읽을까요?\n\n%[1]s",
		},
		dlgIncludeAccept: {en: "Collect again", ko: "다시 수집"},

		dlgClearTitle: {en: "Delete usage data", ko: "사용 분석 자료 지우기"},
		dlgClearMessage: {
			en: "Delete all collected usage data and extension events and stop collecting? Deleted data cannot be restored. The original session records are kept.",
			ko: "모은 사용 분석 자료와 확장 이벤트를 모두 지우고 수집을 끌까요? 지운 자료는 되돌릴 수 없습니다. 원본 세션 기록은 그대로 둡니다.",
		},
		dlgClearAccept: {en: "Delete", ko: "지우기"},
	}
})

// dialogText words native dialogs in one locale. Callers take one value per
// dialog, so the buttons they compare the answer with are the ones shown
// even if the locale changes while the dialog is open.
type dialogText struct{ locale usage.Locale }

func textFor(l usage.Locale) dialogText { return dialogText{usage.NormalizeLocale(l)} }

// get returns the text of key, falling back to the default locale and then
// to the key itself.
func (d dialogText) get(key dialogKey) string {
	row := dialogCatalog()[key]
	if s, ok := row[d.locale]; ok {
		return s
	}
	if s, ok := row[usage.LocaleDefault]; ok {
		return s
	}
	return string(key)
}

// format fills the indexed verbs of key with args.
func (d dialogText) format(key dialogKey, args ...any) string {
	return fmt.Sprintf(d.get(key), args...)
}
