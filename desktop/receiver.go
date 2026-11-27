package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/ingress"
	"github.com/0xmhha/trace-agent/internal/observer"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const receiverInstance = "local:claude-otlp"
const codexReceiverInstance = "local:codex-otlp"
const receiverService = "org.0xmhha.trace-agent.sender."

type localReceiver interface {
	Port() int
	Close(context.Context) error
}

func (s selectedSource) client() string { return collect.ClientCode(s.Kind) }
func (s selectedSource) observerKind() collect.Kind {
	if collect.IsObserver(s.Kind) {
		return s.Kind
	}
	return observer.Hook
}
func (c *collector) parentSource(id string) (*collect.Adapter, error) {
	for _, src := range c.config.Sources {
		if !collect.CanParent(src.Kind) {
			continue
		}
		a, err := c.adapter(src)
		if err != nil {
			return nil, err
		}
		if a.Source() == id {
			return a, nil
		}
	}
	return nil, errSourceParentMissing
}

func (s selectedSource) receiver() bool {
	return s.Kind == collect.Telemetry && s.Path == receiverInstance || s.Kind == collect.CodexTelemetry && s.Path == codexReceiverInstance || s.Parent != ""
}

func receiverToken(installationKey []byte, source string) string {
	h := hmac.New(sha256.New, installationKey)
	h.Write([]byte("otlp-authorization/" + source))
	return hex.EncodeToString(h.Sum(nil))
}

func (c *collector) bindReceiver(a *collect.Adapter, src selectedSource) (localReceiver, error) {
	token := receiverToken(c.config.Key, a.Source())
	if src.Parent != "" {
		parent, err := c.parentSource(src.Parent)
		if err != nil {
			return nil, err
		}
		r, err := observer.Listen(c.db, parent, src.observerKind(), src.Server, src.Port, token)
		if err != nil {
			return nil, err
		}
		return r, nil
	}
	r, err := ingress.Listen(c.db, a, src.Port, token)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func closeReceiver(receiver localReceiver) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = receiver.Close(ctx)
}

// Keep the configured port across restarts; clients already export to this URL.
func (c *collector) startReceiver(a *collect.Adapter, src selectedSource, receiver localReceiver) {
	c.running[a.Source()] = true
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for receiver == nil {
			var err error
			c.mu.Lock()
			receiver, err = c.bindReceiver(a, src)
			c.mu.Unlock()
			if err == nil {
				break
			}
			_ = c.db.ReportHealth(c.ctx, a, "lost")
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-c.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		<-c.ctx.Done()
		closeReceiver(receiver)
	}()
}

func (c *collector) receiverSource(id string) (selectedSource, []byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, src := range c.config.Sources {
		if !src.receiver() {
			continue
		}
		a, err := c.adapter(src)
		if err == nil && a.Source() == id {
			return src, c.config.Key, true
		}
	}
	return selectedSource{}, nil, false
}

var serverName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// PickObserver requests native consent for one client-bound sender.
func (b *Bridge) PickObserver(id, kind, name string) (string, error) {
	c := b.collector
	c.mu.Lock()
	parent, err := c.parentSource(id)
	c.mu.Unlock()
	if err != nil {
		return "", asBridgeError(err, errSourceParentMissing)
	}
	src := selectedSource{Parent: id, Path: "local:observer"}
	text := textFor(b.locale.get())
	var label string
	switch {
	case kind == "hook" && collect.AcceptsParent(collect.ClaudeHook, parent.Kind()) && name == "":
		src.Kind = collect.ClaudeHook
		label = text.get(dlgReceiverHook)
	case kind == "mcp" && serverName.MatchString(name):
		src.Kind = collect.MCPObserver
		src.Server = name
		label = text.format(dlgReceiverMCP, name)
	default:
		return "", errReceiverInvalid
	}
	allowed, err := confirmReceiver(b.ctx, text, text.format(dlgReceiverOwner, label))
	if err != nil || !allowed {
		return "", err
	}
	return c.offer(src)
}

// confirmReceiver asks whether to connect a local receiver. Cancel is the
// default button, so that a stray key press never opens an endpoint.
func confirmReceiver(ctx context.Context, text dialogText, message string) (bool, error) {
	connect, cancel := text.get(dlgConnect), text.get(dlgCancel)
	answer, err := wruntime.MessageDialog(ctx, wruntime.MessageDialogOptions{Type: wruntime.QuestionDialog, Title: text.get(dlgReceiverTitle), Message: message, Buttons: []string{connect, cancel}, DefaultButton: cancel, CancelButton: cancel})
	if err != nil {
		return false, errReceiverDialog
	}
	return answer == connect, nil
}

// HasReceiver identifies a local OTLP endpoint without exposing its credentials.
func (b *Bridge) HasReceiver(id string) bool {
	_, _, ok := b.collector.receiverSource(id)
	return ok
}

// CopyReceiverSettings copies local exporter configuration. The pasted shell
// reads the sender credential from its own Keychain entry at execution time.
func (b *Bridge) CopyReceiverSettings(id string) error {
	src, key, ok := b.collector.receiverSource(id)
	if !ok {
		return errReceiverNotFound
	}
	cfg, err := b.collector.db.SourceConfig(b.ctx, id)
	if err != nil || !cfg.Enabled {
		return errReceiverSourceOff
	}
	if err := saveSenderCredential(receiverService+id, receiverToken(key, id)); err != nil {
		return errReceiverCredential
	}
	settings := senderSettings(src, id, cfg.Consent)
	if err := wruntime.ClipboardSetText(b.ctx, settings); err != nil {
		return errReceiverClipboard
	}
	return nil
}

func senderSettings(src selectedSource, id string, consent bool) string {
	lookup := fmt.Sprintf("$(/usr/bin/security find-generic-password -a sender -s '%s' -w)", receiverService+id)
	if src.Kind == collect.CodexTelemetry {
		return fmt.Sprintf(`export OTEL_EXPORTER_OTLP_LOGS_HEADERS="Authorization=Bearer%%20%s"
codex -c 'otel.exporter.otlp-http.endpoint="http://127.0.0.1:%d/v1/logs"' -c 'otel.exporter.otlp-http.protocol="json"' -c 'otel.log_user_prompt=%t'`, lookup, src.Port, consent)
	}
	if src.Kind == collect.MCPObserver {
		return fmt.Sprintf("export TRACE_MCP_OBSERVER_URL='http://127.0.0.1:%d/v1/mcp'\nexport TRACE_MCP_OBSERVER_TOKEN=\"%s\"", src.Port, lookup)
	}
	if src.Kind == collect.ClaudeHook {
		httpHook := map[string]any{"type": "http", "url": fmt.Sprintf("http://127.0.0.1:%d/v1/hooks", src.Port), "timeout": 1, "onFailure": "continue", "headers": map[string]string{"Authorization": "Bearer ${TRACE_HOOK_TOKEN}"}, "allowedEnvVars": []string{"TRACE_HOOK_TOKEN"}}
		hooks := map[string]any{}
		for _, event := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure"} {
			hooks[event] = []any{map[string]any{"hooks": []any{httpHook}}}
		}
		settings, _ := json.Marshal(map[string]any{"hooks": hooks})
		return fmt.Sprintf("export TRACE_HOOK_TOKEN=\"%s\"\nclaude --settings '%s'", lookup, settings)
	}
	raw := "0"
	if consent {
		raw = "1"
	}
	return fmt.Sprintf(`export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_LOGS_EXPORTER=otlp
export OTEL_METRICS_EXPORTER=none
export OTEL_TRACES_EXPORTER=none
export OTEL_EXPORTER_OTLP_LOGS_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_LOGS_ENDPOINT='http://127.0.0.1:%d/v1/logs'
export OTEL_EXPORTER_OTLP_LOGS_HEADERS="Authorization=Bearer%%20%s"
export OTEL_LOG_RAW_API_BODIES=%s`, src.Port, lookup, raw)
}
