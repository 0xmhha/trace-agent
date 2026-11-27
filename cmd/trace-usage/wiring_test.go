package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
	"github.com/0xmhha/trace-agent/internal/usage/service"
)

// Compile-time checks that the real pipeline fits the command's seams.
var (
	_ usageService = (*service.Service)(nil)
	_ openFunc     = openPipeline
)

func TestProductionDepsAreComplete(t *testing.T) {
	var stdout, stderr bytes.Buffer
	d := productionDeps(&stdout, &stderr)
	if d.open == nil || d.now == nil || d.ticks == nil {
		t.Fatalf("production dependencies are incomplete: %+v", d)
	}
	if d.location != time.Local {
		t.Errorf("location = %v, want the local zone", d.location)
	}
	if d.tempBase != "" {
		t.Errorf("temporary base = %q, want the system default", d.tempBase)
	}
	if d.stdout != &stdout || d.stderr != &stderr {
		t.Error("output writers are not the ones passed in")
	}
	env := processEnvironment()
	if env.getenv == nil || env.homeDir == nil || env.configDir == nil {
		t.Errorf("process environment is incomplete: %+v", env)
	}
}

func TestTickEveryStops(t *testing.T) {
	ticks, stop := tickEvery(time.Hour)
	stop()
	select {
	case <-ticks:
		t.Error("a stopped hourly ticker fired")
	default:
	}
}

// The root is checked before the database is created, so these cases hold
// whatever the store and the service behind openPipeline do.
func TestOpenPipelineRejectsBadRoot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		root    string
		wantErr error
	}{
		{name: "missing root", root: filepath.Join(dir, "missing"), wantErr: fs.ErrNotExist},
		{name: "root is a file", root: file, wantErr: errRootNotDirectory},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), tempDBName)
			svc, closeStore, err := openPipeline(t.Context(), openParams{
				root:      tt.root,
				rootLabel: tt.root,
				dbPath:    dbPath,
				days:      usage.DefaultWindowDays,
				now:       time.Now,
				location:  time.UTC,
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if svc != nil || closeStore != nil {
				t.Error("a failed open returned a service or a close function")
			}
			if _, statErr := os.Stat(dbPath); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("database created for a bad root: %v", statErr)
			}
		})
	}
}
