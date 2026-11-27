package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

const (
	// extPort is the port the scripted endpoint binds.
	extPort = 4100
	// logSwitch prefixes the endpoint calls in the store's call log, so that
	// a test sees the order of store writes and endpoint switches.
	logSwitch = "SetEnabled:"
)

// orderedExtension is an ExtensionControl that writes its calls into the
// fake store's log. errs scripts the error of each call by its index.
type orderedExtension struct {
	store *fakeStore

	mu      sync.Mutex
	enabled bool
	calls   int
	errs    map[int]error
}

func (e *orderedExtension) SetEnabled(_ context.Context, enabled bool) (int, error) {
	e.store.with(func(f *fakeStore) { f.log = append(f.log, logSwitch+strconv.FormatBool(enabled)) })
	e.mu.Lock()
	defer e.mu.Unlock()
	n := e.calls
	e.calls++
	if err := e.errs[n]; err != nil {
		return 0, err
	}
	e.enabled = enabled
	return extPort, nil
}

func (e *orderedExtension) Endpoint() usage.ExtEndpoint {
	e.mu.Lock()
	defer e.mu.Unlock()
	return usage.ExtEndpoint{Enabled: e.enabled}
}

func (e *orderedExtension) live() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enabled
}

// extensionHarness is a settled harness whose endpoint is on and whose store
// holds that setting.
func extensionHarness(t *testing.T, startOn bool) (*harness, *orderedExtension) {
	t.Helper()
	var extension *orderedExtension
	h := settled(t, func(cfg *Config) {
		extension = &orderedExtension{store: cfg.Store.(*fakeStore), errs: map[int]error{}}
		cfg.Extension = extension
	})
	if startOn {
		extension.enabled = true
	}
	h.store.with(func(f *fakeStore) {
		f.settings.ExtEnabled, f.settings.ExtPort = startOn, extPort
		f.log = nil
	})
	return h, extension
}

func TestHandleSetExtensionKeepsStoreAndEndpointInAgreement(t *testing.T) {
	for _, tc := range []struct {
		name    string
		startOn bool
		params  string
		// storeFails makes Store.SetExtension fail; switchErrs scripts the
		// endpoint calls by index.
		storeFails  bool
		switchErrs  map[int]error
		wantErr     bool
		wantLog     []string
		wantStored  bool
		wantLive    bool
		wantStoreOn bool
	}{
		{
			name: "switching off stores the off state before stopping", startOn: true, params: `{"enabled":false}`,
			wantLog:    []string{"SetExtension", logSwitch + "false"},
			wantStored: true,
		},
		{
			name: "switching off stops the endpoint although storing fails", startOn: true, params: `{"enabled":false}`,
			storeFails: true, wantErr: true,
			wantLog:     []string{logSwitch + "false"},
			wantStoreOn: true,
		},
		{
			name: "switching off reports an endpoint that does not stop", startOn: true, params: `{"enabled":false}`,
			switchErrs: map[int]error{0: errInjected}, wantErr: true,
			wantLog:    []string{"SetExtension", logSwitch + "false"},
			wantStored: true, wantLive: true,
		},
		{
			name: "switching on starts the endpoint, then stores the port", params: `{"enabled":true}`,
			wantLog:    []string{logSwitch + "true", "SetExtension"},
			wantStored: true, wantLive: true, wantStoreOn: true,
		},
		{
			name: "switching on stops the endpoint again when storing fails", params: `{"enabled":true}`,
			storeFails: true, wantErr: true,
			wantLog: []string{logSwitch + "true", logSwitch + "false"},
		},
		{
			name: "switching on reports a failed start and stores nothing", params: `{"enabled":true}`,
			switchErrs: map[int]error{0: errInjected}, wantErr: true,
			wantLog: []string{logSwitch + "true"},
		},
		{
			name: "switching on reports both a failed store and a failed stop", params: `{"enabled":true}`,
			storeFails: true, switchErrs: map[int]error{1: errInjected}, wantErr: true,
			wantLog:  []string{logSwitch + "true", logSwitch + "false"},
			wantLive: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, extension := extensionHarness(t, tc.startOn)
			extension.errs = tc.switchErrs
			if tc.storeFails {
				h.store.with(func(f *fakeStore) { f.fail["SetExtension"] = errInjected })
			}
			_, err := h.svc.Handle(context.Background(), usage.MethodSetExtension, json.RawMessage(tc.params))
			if tc.wantErr != (err != nil) || (err != nil && !errors.Is(err, errInjected)) {
				t.Fatalf("Handle = %v; want error %v", err, tc.wantErr)
			}
			h.store.with(func(f *fakeStore) {
				if !slices.Equal(f.log, tc.wantLog) {
					t.Errorf("calls = %v\nwant %v", f.log, tc.wantLog)
				}
				if stored := len(f.extensionSets) > 0; stored != tc.wantStored {
					t.Errorf("stored %+v; want a stored setting %v", f.extensionSets, tc.wantStored)
				}
				if f.settings.ExtEnabled != tc.wantStoreOn || f.settings.ExtPort != extPort {
					t.Errorf("stored setting on=%v port=%d; want on=%v port=%d", f.settings.ExtEnabled, f.settings.ExtPort, tc.wantStoreOn, extPort)
				}
			})
			if got := extension.live(); got != tc.wantLive {
				t.Errorf("endpoint live = %v; want %v", got, tc.wantLive)
			}
		})
	}
}

// A failure to read the settings before storing the off state still stops
// the endpoint.
func TestHandleSetExtensionOffWithUnreadableSettings(t *testing.T) {
	h, extension := extensionHarness(t, true)
	h.store.with(func(f *fakeStore) { f.fail["Settings"] = errInjected })
	if _, err := h.svc.Handle(context.Background(), usage.MethodSetExtension, json.RawMessage(`{"enabled":false}`)); !errors.Is(err, errInjected) {
		t.Fatalf("Handle = %v; want the settings failure", err)
	}
	if extension.live() {
		t.Fatal("endpoint still live after switching off")
	}
}

func TestHandleClearDataSwitchesExtensionOff(t *testing.T) {
	t.Run("in order", func(t *testing.T) {
		h, extension := extensionHarness(t, true)
		status := call[usage.Status](t, h.svc, usage.MethodClearData, ``)
		if status.Extension.Enabled || extension.live() {
			t.Fatalf("endpoint after clearData = %+v; want off", status.Extension)
		}
		h.store.with(func(f *fakeStore) {
			want := []string{"SetConsent:false", "SetExtension", logSwitch + "false", "Clear"}
			if !slices.Equal(f.log, want) {
				t.Fatalf("calls = %v\nwant %v", f.log, want)
			}
			if f.settings.ExtEnabled || f.settings.ExtPort != extPort {
				t.Fatalf("stored setting on=%v port=%d; want off with the last port", f.settings.ExtEnabled, f.settings.ExtPort)
			}
		})
	})

	t.Run("storing the off state fails", func(t *testing.T) {
		h, extension := extensionHarness(t, true)
		h.store.with(func(f *fakeStore) { f.fail["SetExtension"] = errInjected })
		if _, err := h.svc.Handle(context.Background(), usage.MethodClearData, nil); !errors.Is(err, errInjected) {
			t.Fatalf("clearData = %v; want the store failure", err)
		}
		if extension.live() {
			t.Fatal("endpoint still live after clearData")
		}
		h.store.with(func(f *fakeStore) {
			// The data is deleted all the same.
			want := []string{"SetConsent:false", logSwitch + "false", "Clear"}
			if !slices.Equal(f.log, want) {
				t.Fatalf("calls = %v\nwant %v", f.log, want)
			}
		})
	})

	t.Run("clearing fails as well", func(t *testing.T) {
		h, _ := extensionHarness(t, true)
		clearErr := errors.New("clear failure")
		h.store.with(func(f *fakeStore) {
			f.fail["SetExtension"] = errInjected
			f.fail["Clear"] = clearErr
		})
		_, err := h.svc.Handle(context.Background(), usage.MethodClearData, nil)
		if !errors.Is(err, errInjected) || !errors.Is(err, clearErr) {
			t.Fatalf("clearData = %v; want both failures", err)
		}
	})
}

func TestCtxReader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newCtxReader(ctx, bytes.NewReader([]byte("abcdef")))
	buf := make([]byte, 3)
	if n, err := r.Read(buf); n != 3 || err != nil {
		t.Fatalf("Read before cancel = %d, %v", n, err)
	}
	cancel()
	if n, err := r.Read(buf); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("Read after cancel = %d, %v; want 0, context.Canceled", n, err)
	}
	if pos, err := r.Seek(1, io.SeekStart); pos != 1 || err != nil {
		t.Fatalf("Seek = %d, %v", pos, err)
	}
}

// A timeline read stops at the request's end instead of reading on, and the
// request reports the context error rather than an unreadable transcript.
func TestHandleTimelineStopsWhenTheRequestEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var after error
	h := settled(t, func(cfg *Config) {
		cfg.Timeline = func(r io.ReadSeeker, _ usage.TimelineRequest) (usage.TimelinePage, error) {
			buf := make([]byte, 1)
			if _, err := r.Read(buf); err != nil {
				return usage.TimelinePage{}, err
			}
			cancel()
			_, after = r.Read(buf)
			return usage.TimelinePage{}, after
		}
	})
	_, err := h.svc.Handle(ctx, usage.MethodTimeline, json.RawMessage(sessionParams))
	if !errors.Is(after, context.Canceled) {
		t.Fatalf("read after the request ended = %v; want context.Canceled", after)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Handle = %v; want context.Canceled", err)
	}
}
