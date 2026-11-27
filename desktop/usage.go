package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/0xmhha/trace-agent/internal/extension"
	"github.com/0xmhha/trace-agent/internal/usage"
	usagedb "github.com/0xmhha/trace-agent/internal/usage/db"
	"github.com/0xmhha/trace-agent/internal/usage/insight"
	"github.com/0xmhha/trace-agent/internal/usage/service"
	"github.com/0xmhha/trace-agent/internal/usage/timeline"
	"github.com/0xmhha/trace-agent/internal/usage/transcript"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	usageDatabaseFile = "usage.sqlite"
	// claudeConfigDirEnv relocates the Claude Code configuration directory;
	// the transcripts then live in its projects directory.
	claudeConfigDirEnv     = "CLAUDE_CONFIG_DIR"
	claudeDefaultConfigDir = ".claude"
	claudeProjectsDir      = "projects"
	homeLabel              = "~"
	// usageStartTimeout bounds opening the database and reading its settings,
	// so that a locked file cannot keep the window from appearing.
	usageStartTimeout = 10 * time.Second
	// The notices name no path and repeat no internal error text; the
	// process log may be read by other people than the user.
	usageStartNotice    = "사용 분석을 시작하지 못했습니다. 추적 기능은 그대로 동작합니다.\n"
	usageReceiverNotice = "확장 이벤트 수신기를 다시 열지 못했습니다. 수집 설정에서 다시 켜 주세요.\n"
)

// usageService is the part of the usage service the desktop process drives.
type usageService interface {
	Run(ctx context.Context) error
	SetConsent(ctx context.Context, granted bool) error
	Handle(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error)
}

// usageStore is the usage database as its owner sees it.
type usageStore interface {
	service.Store
	Close() error
}

// usageReceiver is a bound extension event endpoint.
type usageReceiver interface {
	URL() string
	Port() int
	Close(ctx context.Context) error
}

type (
	usageListenFunc      func(sink usage.ExtSink, port int, bearer string) (usageReceiver, error)
	usageDialogFunc      func(ctx context.Context, options wruntime.MessageDialogOptions) (string, error)
	usageClipboardFunc   func(ctx context.Context, text string) error
	usageCredentialSaver func(service, credential string) error
)

// usageDeps are the seams between the usage runtime and the operating
// system, the Keychain and the usage packages. Production wiring is
// productionUsageDeps; tests substitute fakes.
type usageDeps struct {
	openStore       func(ctx context.Context, path string) (usageStore, error)
	newService      func(cfg service.Config) (usageService, error)
	listen          usageListenFunc
	installationKey func() ([]byte, error)
	getenv          func(string) string
	homeDir         func() (string, error)
	now             func() time.Time
	location        *time.Location
	dialog          usageDialogFunc
	clipboard       usageClipboardFunc
	saveCredential  usageCredentialSaver
}

// productionUsageDeps wires the real store, service, endpoint and native
// dialogs. Each adapter returns a nil interface on failure; passing the
// concrete nil pointer through would make the result look usable.
func productionUsageDeps(c *collector) usageDeps {
	return usageDeps{
		openStore: func(ctx context.Context, path string) (usageStore, error) {
			s, err := usagedb.Open(ctx, path, time.Now)
			if err != nil {
				return nil, err
			}
			return s, nil
		},
		newService: func(cfg service.Config) (usageService, error) {
			s, err := service.New(cfg)
			if err != nil {
				return nil, err
			}
			return s, nil
		},
		listen: func(sink usage.ExtSink, port int, bearer string) (usageReceiver, error) {
			r, err := extension.Listen(sink, port, bearer, time.Now)
			if err != nil {
				return nil, err
			}
			return r, nil
		},
		installationKey: func() ([]byte, error) { return c.ensureInstallationKey(rand.Reader) },
		getenv:          os.Getenv,
		homeDir:         os.UserHomeDir,
		now:             time.Now,
		location:        time.Local,
		dialog:          wruntime.MessageDialog,
		clipboard:       wruntime.ClipboardSetText,
		saveCredential:  saveSenderCredential,
	}
}

// usageRuntime owns the usage pipeline of the desktop process: the store,
// the service with its scan goroutine, and the extension endpoint. A runtime
// whose start-up failed keeps service nil; the bridge then refuses every
// usage request while the trace features keep working.
type usageRuntime struct {
	service        usageService
	extension      *extensionControl
	rootLabel      string
	dialog         usageDialogFunc
	clipboard      usageClipboardFunc
	saveCredential usageCredentialSaver

	// startErr is why the pipeline is unavailable. receiverErr is why an
	// endpoint that was enabled before could not be restored; the pipeline
	// itself is then still available.
	startErr    error
	receiverErr error

	store    usageStore
	cancel   context.CancelFunc
	scanDone chan error
	closing  sync.Once
	closeErr error
}

// startUsage brings the usage pipeline up under dir. It never fails the
// application: the returned runtime is always usable by the bridge and
// reports its own state.
func startUsage(ctx context.Context, dir string, deps usageDeps) *usageRuntime {
	u := &usageRuntime{dialog: deps.dialog, clipboard: deps.clipboard, saveCredential: deps.saveCredential}
	u.startErr = u.start(ctx, dir, deps)
	return u
}

func (u *usageRuntime) start(ctx context.Context, dir string, deps usageDeps) (err error) {
	root, label, err := usageRoot(deps.getenv, deps.homeDir)
	if err != nil {
		return err
	}
	startCtx, cancelStart := context.WithTimeout(ctx, usageStartTimeout)
	defer cancelStart()
	store, err := deps.openStore(startCtx, filepath.Join(dir, usageDatabaseFile))
	if err != nil {
		return fmt.Errorf("open usage store: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, store.Close())
		}
	}()
	settings, err := store.Settings(startCtx)
	if err != nil {
		return fmt.Errorf("read usage settings: %w", err)
	}
	control := newExtensionControl(store, deps.listen, deps.installationKey, settings.ExtPort)
	svc, err := deps.newService(service.Config{
		Store:     store,
		Root:      root,
		RootLabel: label,
		Scan:      transcript.Scan,
		Timeline:  timeline.Read,
		Evaluate:  insight.Evaluate,
		Now:       deps.now,
		Location:  deps.location,
		Extension: control,
	})
	if err != nil {
		return fmt.Errorf("create usage service: %w", err)
	}
	if settings.ExtEnabled {
		u.receiverErr = restoreExtension(startCtx, store, control, settings.ExtPort)
	}
	scanCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- svc.Run(scanCtx) }()
	u.service, u.extension, u.rootLabel = svc, control, label
	u.store, u.cancel, u.scanDone = store, cancel, done
	return nil
}

// restoreExtension reopens an endpoint the user enabled in an earlier run. A
// stored port that is now taken yields a new one, which is recorded so that
// the next start tries the port senders were last told about.
func restoreExtension(ctx context.Context, store usageStore, control *extensionControl, storedPort int) error {
	port, err := control.SetEnabled(ctx, true)
	if err != nil {
		return fmt.Errorf("restore extension endpoint: %w", err)
	}
	if port == storedPort {
		return nil
	}
	if err = store.SetExtension(ctx, true, port); err != nil {
		return fmt.Errorf("record extension port: %w", err)
	}
	return nil
}

// report writes one line per start-up problem to w, the process log. The
// usage pages show the same state through the bridge; this line is for
// whoever starts the app from a terminal.
func (u *usageRuntime) report(w io.Writer) {
	if u.startErr != nil {
		_, _ = io.WriteString(w, usageStartNotice)
	}
	if u.receiverErr != nil {
		_, _ = io.WriteString(w, usageReceiverNotice)
	}
}

// close stops the pipeline in dependency order: the endpoint first so that
// nothing new reaches the store, then the scan, and the store last once
// neither can write to it. It is safe to call on a runtime that never
// started and to call more than once.
func (u *usageRuntime) close() error {
	if u == nil {
		return nil
	}
	u.closing.Do(func() {
		if u.store == nil {
			return
		}
		u.extension.shutdown()
		u.cancel()
		scanErr := <-u.scanDone
		if errors.Is(scanErr, context.Canceled) {
			scanErr = nil
		}
		if scanErr != nil {
			scanErr = fmt.Errorf("usage scan: %w", scanErr)
		}
		closeErr := u.store.Close()
		if closeErr != nil {
			closeErr = fmt.Errorf("close usage store: %w", closeErr)
		}
		u.closeErr = errors.Join(scanErr, closeErr)
	})
	return u.closeErr
}

// usageRoot resolves the transcript root and its display form. The label
// shows the home directory as "~" so that dialogs and pages do not print the
// account name.
func usageRoot(getenv func(string) string, homeDir func() (string, error)) (root, label string, err error) {
	home, homeErr := homeDir()
	if homeErr == nil && !filepath.IsAbs(home) {
		homeErr = errors.New("home directory is not absolute")
	}
	if dir := getenv(claudeConfigDirEnv); dir != "" {
		if root, err = filepath.Abs(filepath.Join(dir, claudeProjectsDir)); err != nil {
			return "", "", fmt.Errorf("resolve transcript root: %w", err)
		}
	} else {
		if homeErr != nil {
			return "", "", fmt.Errorf("resolve transcript root: %w", homeErr)
		}
		root = filepath.Join(home, claudeDefaultConfigDir, claudeProjectsDir)
	}
	if homeErr != nil {
		return root, root, nil
	}
	return root, homeRelative(root, filepath.Clean(home)), nil
}

// homeRelative replaces a leading home directory with "~". A sibling whose
// name merely starts with the home directory's name is left alone.
func homeRelative(path, home string) string {
	if path == home {
		return homeLabel
	}
	prefix := strings.TrimSuffix(home, string(filepath.Separator)) + string(filepath.Separator)
	if rest, ok := strings.CutPrefix(path, prefix); ok {
		return homeLabel + string(filepath.Separator) + rest
	}
	return path
}
