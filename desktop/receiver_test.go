package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
	"github.com/0xmhha/trace-agent/internal/transport"
)

type failingVault struct{ testVault }

func (*failingVault) Save(registry) error { return errors.New("test keychain locked") }

func TestFailedSelectionAndRecovery(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "activity.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	c, err := newCollector(ctx, db, &failingVault{})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "test.jsonl")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	ticket, err := c.offer(selectedSource{Kind: collect.Transcript, Path: file})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.consume(ctx, transport.PickerAction{Operation: "attachSource", PickerToken: ticket, Client: "CLAUDE", RetentionDays: 7})
	if err == nil {
		t.Fatal("keychain error lost")
	}
	point, err := db.Point(ctx)
	if err != nil || point.Revision != 0 || len(c.running) != 0 {
		t.Fatal("failed attach became visible", point, err)
	}
	// A process can stop after Keychain staging but before the SQLite commit.
	v := &testVault{r: registry{Key: make([]byte, 32), Sources: []selectedSource{{Kind: collect.Transcript, Path: file}}}}
	recovered, err := newCollector(ctx, db, v)
	if err != nil || len(recovered.running) != 0 {
		t.Fatal("orphaned selection started a reader", err)
	}
}

func TestReceiverSetupAndRestart(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(dir, "activity.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	v := &testVault{}
	c, err := newCollector(ctx, db, v)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); c.wg.Wait() }()
	src := selectedSource{Kind: collect.Telemetry, Path: receiverInstance}
	ticket, err := c.offer(src)
	if err != nil {
		t.Fatal(err)
	}
	act := transport.PickerAction{Operation: "attachSource", PickerToken: ticket, Client: "CLAUDE", RetentionDays: 7}
	point, err := c.consume(ctx, act)
	if err != nil || point.Revision != 1 {
		t.Fatal("receiver attach", point, err)
	}
	if _, err := c.consume(ctx, act); err == nil {
		t.Fatal("picker ticket reused")
	}
	a, err := c.adapter(src)
	if err != nil {
		t.Fatal(err)
	}
	id := a.Source()
	bound, key, ok := c.receiverSource(id)
	if !ok || bound.Port == 0 {
		t.Fatal("missing receiver address")
	}
	secret := receiverToken(key, id)
	for _, kind := range []collect.Kind{collect.Telemetry, collect.CodexTelemetry, collect.ClaudeHook, collect.MCPObserver} {
		s := bound
		s.Kind = kind
		script := senderSettings(s, id, false)
		if strings.Contains(script, secret) || !strings.Contains(script, receiverService+id) || !strings.Contains(script, "127.0.0.1:") {
			t.Fatal("unsafe sender configuration")
		}
		if kind == collect.Telemetry && (!strings.Contains(script, "OTEL_LOG_RAW_API_BODIES=0") || !strings.Contains(senderSettings(s, id, true), "OTEL_LOG_RAW_API_BODIES=1")) {
			t.Fatal("incorrect raw body opt-in")
		}
	}
	cancel()
	c.wg.Wait()
	ctx2, stop := context.WithCancel(t.Context())
	defer stop()
	c2, err := newCollector(ctx2, db, v)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { stop(); c2.wg.Wait() }()
	restored, key2, ok := c2.receiverSource(id)
	if !ok || restored.Port != bound.Port || receiverToken(key2, id) != secret {
		t.Fatal("receiver address or credential changed")
	}
	checkCtx, stopCheck := context.WithTimeout(ctx2, 5*time.Second)
	defer stopCheck()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(checkCtx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(restored.Port)))
		if err == nil {
			_ = conn.Close()
			break
		}
		select {
		case <-tick.C:
		case <-checkCtx.Done():
			t.Fatal("receiver did not resume")
		}
	}
}
