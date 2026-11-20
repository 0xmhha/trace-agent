package main

import (
	"context"
	"crypto/rand"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/observer"
	"github.com/0xmhha/trace-agent/internal/store"
	"github.com/0xmhha/trace-agent/internal/transport"
)

type selectedSource struct {
	Parent, Server string
	Kind           collect.Kind
	Path           string
	Folder         bool
	Port           int
}
type registry struct {
	Key     []byte
	Sources []selectedSource
}
type vault interface {
	Load() (registry, error)
	Save(registry) error
}
type capability struct {
	selection selectedSource
	expires   time.Time
}
type collector struct {
	ctx     context.Context
	db      *store.Store
	vault   vault
	mu      sync.Mutex
	config  registry
	picks   map[string]capability
	running map[string]bool
	wg      sync.WaitGroup
}

func newCollector(ctx context.Context, db *store.Store, v vault) (*collector, error) {
	r, err := v.Load()
	if err != nil {
		return nil, err
	}
	c := &collector{ctx: ctx, db: db, vault: v, config: r, picks: make(map[string]capability), running: make(map[string]bool)}
	for _, src := range r.Sources {
		a, err := c.adapter(src)
		if err != nil {
			return nil, err
		}
		registered, err := db.Registered(ctx, a.Source())
		if err != nil {
			return nil, err
		}
		// A crash between Keychain staging and SQLite commit leaves no grant.
		if registered {
			c.start(a, src)
		}
	}
	return c, nil
}
func (c *collector) adapter(src selectedSource) (*collect.Adapter, error) {
	if src.Parent != "" {
		parent, err := c.parentSource(src.Parent)
		if err != nil {
			return nil, err
		}
		return observer.Source(parent, src.observerKind(), src.Server)
	}
	return collect.New(src.Kind, "local", src.Path, "", c.config.Key, []string{"Read", "Edit", "Write", "Bash", "Agent", "spawn_agent", "multi_agent_v1.spawn_agent", "multi_agent_v1.send_input", "multi_agent_v1.wait_agent", "multi_agent_v1.resume_agent", "multi_agent_v1.close_agent", "send_input", "wait_agent", "resume_agent", "close_agent", "spawnAgent", "sendInput", "wait", "resumeAgent", "closeAgent", "exec_command"}, time.Now)
}
func (c *collector) offer(src selectedSource) (string, error) {
	if !src.receiver() {
		p, err := filepath.EvalSymlinks(src.Path)
		if err != nil || !filepath.IsAbs(p) {
			return "", errors.New("선택한 경로를 확인할 수 없습니다")
		}
		src.Path = p
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.picks {
		if !v.expires.After(now) {
			delete(c.picks, k)
		}
	}
	if len(c.picks) >= 16 {
		return "", errors.New("열린 출처 선택이 너무 많습니다")
	}
	ticket := rand.Text()
	c.picks[ticket] = capability{src, now.Add(time.Minute)}
	return ticket, nil
}
func (c *collector) consume(ctx context.Context, p transport.PickerAction) (store.Point, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pick, ok := c.picks[p.PickerToken]
	delete(c.picks, p.PickerToken)
	if !ok || !pick.expires.After(time.Now()) {
		return store.Point{}, errors.New("출처 선택이 만료됐습니다")
	}
	src := pick.selection
	if p.Operation == "attachSource" && p.Client != src.client() {
		return store.Point{}, errors.New("클라이언트 종류가 다릅니다")
	}
	if len(c.config.Key) == 0 {
		c.config.Key = make([]byte, 32)
		if _, err := rand.Read(c.config.Key); err != nil {
			return store.Point{}, err
		}
	}
	a, err := c.adapter(src)
	if err != nil {
		return store.Point{}, err
	}
	if p.Operation == "setHistory" && a.Source() != p.SourceID {
		return store.Point{}, errors.New("같은 출처를 선택해 주세요")
	}
	for _, known := range c.config.Sources {
		if known.Kind == src.Kind && known.Path == src.Path && known.Folder == src.Folder && known.Parent == src.Parent && known.Server == src.Server {
			src = known
			break
		}
	}
	var pending localReceiver
	if src.receiver() && !c.running[a.Source()] {
		pending, err = c.bindReceiver(a, src)
		if err != nil {
			return store.Point{}, err
		}
		src.Port = pending.Port()
		defer func() {
			if pending != nil {
				closeReceiver(pending)
			}
		}()
	}
	updated := c.config
	if !slices.Contains(updated.Sources, src) {
		updated.Sources = append(slices.Clone(updated.Sources), src)
		if err = c.vault.Save(updated); err != nil {
			return store.Point{}, err
		}
	}
	var from, until *int64
	if !p.From.IsZero() {
		start, end := p.From.UnixMilli(), p.Until.UnixMilli()
		from, until = &start, &end
	}
	point, err := c.db.Attach(ctx, a, p.RetentionDays, from, until)
	if err != nil {
		if len(updated.Sources) != len(c.config.Sources) {
			err = errors.Join(err, c.vault.Save(c.config))
		}
		return store.Point{}, err
	}
	c.config = updated
	if pending != nil {
		c.startReceiver(a, src, pending)
		pending = nil
	} else {
		c.start(a, src)
	}
	return point, nil
}
func (c *collector) start(a *collect.Adapter, src selectedSource) {
	if c.running[a.Source()] {
		return
	}
	if src.receiver() {
		c.startReceiver(a, src, nil)
		return
	}
	c.running[a.Source()] = true
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		period := 200 * time.Millisecond
		if src.Kind == collect.AppServer {
			period = 2 * time.Second
		}
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			cfg, err := c.db.SourceConfig(c.ctx, a.Source())
			if err == nil && cfg.Enabled {
				current := a
				if cfg.Consent {
					current = a.WithBodyCollection()
				}
				if src.Kind == collect.AppServer {
					err = c.followCodex(current, src.Path, cfg)
				} else {
					err = c.readFiles(current, src)
				}
				health := "connected"
				if err != nil {
					health = "lost"
				}
				if !errors.Is(err, context.Canceled) {
					_ = c.db.ReportHealth(c.ctx, a, health)
				}
			}
			select {
			case <-c.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// followCodex holds one read-only connection across history refreshes and notices.
// Stop the process promptly if the source is disabled or its grant changes.
func (c *collector) followCodex(a *collect.Adapter, dir string, initial store.SourceConfig) error {
	ctx, cancel := context.WithCancel(c.ctx)
	done := make(chan error, 1)
	synced := make(chan struct{}, 1)
	go func() {
		done <- a.CollectLocalCodex(ctx, collect.AppOptions{Directory: dir, Follow: true, PollInterval: 2 * time.Second, RequestTimeout: 45 * time.Second, Synced: func() {
			select {
			case synced <- struct{}{}:
			default:
			}
		}}, c.db)
	}()
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	same := func(a, b *int64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
	stop := func(err error) error { cancel(); <-done; return err }
	for {
		select {
		case <-ctx.Done():
			return stop(ctx.Err())
		case err := <-done:
			return err
		case <-synced:
			_ = c.db.ReportHealth(ctx, a, "connected")
		case <-ticker.C:
			cfg, err := c.db.SourceConfig(ctx, a.Source())
			if err != nil {
				return stop(err)
			}
			if !cfg.Enabled || cfg.Consent != initial.Consent || !same(cfg.FromMS, initial.FromMS) || !same(cfg.UntilMS, initial.UntilMS) {
				return stop(context.Canceled)
			}
		}
	}
}
func (c *collector) readFiles(a *collect.Adapter, src selectedSource) error {
	base := filepath.Dir(src.Path)
	if src.Folder {
		base = src.Path
	}
	actual, err := filepath.EvalSymlinks(base)
	if err != nil || actual != base {
		return errors.New("출처 디렉터리가 변경됐습니다")
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	read := func(name string) error {
		cursor, err := c.db.Cursor(c.ctx, a.Source(), a.StreamKey(name))
		if err != nil {
			return err
		}
		_, err = a.ReadFile(c.ctx, root, name, cursor, c.db.FileCommit)
		return err
	}
	if !src.Folder {
		return read(filepath.Base(src.Path))
	}
	// A revoked or malformed child file must not starve other selected streams.
	var streamErr error
	walkErr := fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			streamErr = err
			return nil
		}
		if err := c.ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() && d.Type().IsRegular() && strings.HasSuffix(name, ".jsonl") {
			if err := read(name); err != nil {
				streamErr = err
			}
		}
		return nil
	})
	return errors.Join(walkErr, streamErr)
}
