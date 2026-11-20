// Package main runs the macOS Trace Agent desktop application.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
	"github.com/0xmhha/trace-agent/internal/transport"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var files embed.FS

// Bridge exposes bootstrap and the native permission picker to the embedded WebView.
type Bridge struct {
	ctx       context.Context
	server    *transport.Server
	collector *collector
	url       string
}

// BootstrapResult carries a single-use connection ticket in process memory.
type BootstrapResult struct {
	URL    string `json:"url"`
	Ticket string `json:"ticket"`
}

// Bootstrap issues a one-time ticket for the local desktop.
func (b *Bridge) Bootstrap() (BootstrapResult, error) {
	ticket, err := b.server.Ticket(true)
	return BootstrapResult{b.url, ticket}, err
}

// Pick opens an OS dialog and returns an opaque, expiring selection capability.
func (b *Bridge) Pick(kind string) (string, error) {
	src := selectedSource{Kind: collect.Kind(kind)}
	switch kind {
	case "claude-otlp", "codex-otlp":
		src = selectedSource{Kind: collect.Telemetry, Path: receiverInstance}
		label := "Claude Code"
		if kind == "codex-otlp" {
			src = selectedSource{Kind: collect.CodexTelemetry, Path: codexReceiverInstance}
			label = "Codex"
		}
		answer, err := wruntime.MessageDialog(b.ctx, wruntime.MessageDialogOptions{Type: wruntime.QuestionDialog, Title: "로컬 수신기 연결", Message: "이 Mac에서 " + label + "가 보낸 OTel 기록을 받을까요?", Buttons: []string{"연결", "취소"}, DefaultButton: "취소", CancelButton: "취소"})
		if err != nil || answer != "연결" {
			return "", err
		}
		return b.collector.offer(src)
	case "codex-app-server":
		src.Folder = true
	case "claude-project":
		src.Kind = collect.Transcript
		src.Folder = true
	case "claude-transcript", "codex-rollout", "claude-otel":
	default:
		return "", errors.New("알 수 없는 출처입니다")
	}
	var err error
	if src.Folder {
		src.Path, err = wruntime.OpenDirectoryDialog(b.ctx, wruntime.OpenDialogOptions{Title: "수집할 디렉터리 선택", ShowHiddenFiles: true})
	} else {
		src.Path, err = wruntime.OpenFileDialog(b.ctx, wruntime.OpenDialogOptions{Title: "수집할 세션 파일 선택", ShowHiddenFiles: true, Filters: []wruntime.FileFilter{{DisplayName: "세션 기록", Pattern: "*.jsonl"}}})
	}
	if err != nil {
		return "", errors.New("출처를 선택할 수 없습니다")
	}
	if src.Path == "" {
		return "", nil
	}
	return b.collector.offer(src)
}

// PickHistory reopens the same picker type used to grant the original source.
func (b *Bridge) PickHistory(id string) (string, error) {
	c := b.collector
	c.mu.Lock()
	var kind string
	for _, src := range c.config.Sources {
		if src.receiver() {
			continue
		}
		a, err := c.adapter(src)
		if err == nil && a.Source() == id {
			kind = string(src.Kind)
			if src.Kind == collect.Transcript && src.Folder {
				kind = "claude-project"
			}
			break
		}
	}
	c.mu.Unlock()
	if kind == "" {
		return "", errors.New("이력 범위를 다시 선택할 수 없는 출처입니다")
	}
	return b.Pick(kind)
}
func main() {
	if err := run(); err != nil {
		_, _ = os.Stderr.WriteString("Trace Agent를 시작하지 못했습니다. 로컬 저장소와 키체인 권한을 확인해 주세요.\n")
		os.Exit(1)
	}
}
func runDesktop(dir string, storage vault, id string, ready func(context.Context, *Bridge)) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(dir, "activity.sqlite"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err = db.Expire(ctx, time.Now()); err != nil {
		return err
	}
	server, err := transport.New(db, "wails://wails")
	if err != nil {
		return err
	}
	c, err := newCollector(ctx, db, storage)
	if err != nil {
		return err
	}
	defer func() { cancel(); c.wg.Wait() }()
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		tick := time.NewTicker(time.Hour)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				_ = db.Expire(ctx, now)
			}
		}
	}()
	server.SetPicker(c.consume)
	url, err := server.Listen()
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = server.Close(shutdown)
	}()
	b := &Bridge{server: server, collector: c, url: url}
	assets, err := fs.Sub(files, "frontend/dist")
	if err != nil {
		return err
	}
	return wails.Run(&options.App{Title: "Trace Agent", Width: 1220, Height: 800, MinWidth: 640, MinHeight: 480, BackgroundColour: options.NewRGB(245, 245, 241), AssetServer: &assetserver.Options{Assets: assets}, OnStartup: func(ctx context.Context) { b.ctx = ctx }, OnDomReady: func(ctx context.Context) {
		if ready != nil {
			ready(ctx, b)
		}
	}, Bind: []interface{}{b}, SingleInstanceLock: &options.SingleInstanceLock{UniqueId: id}})
}
