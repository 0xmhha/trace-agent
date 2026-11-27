package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

var errNoDirectory = errors.New("no such directory")

// testEnvironment is a process environment with the given directories. An
// empty home or configuration directory makes its lookup fail.
func testEnvironment(home, claudeConfigDir, configDir string) environment {
	lookup := func(dir string) func() (string, error) {
		return func() (string, error) {
			if dir == "" {
				return "", errNoDirectory
			}
			return dir, nil
		}
	}
	return environment{
		getenv: func(key string) string {
			if key == envConfigDir {
				return claudeConfigDir
			}
			return ""
		},
		homeDir:   lookup(home),
		configDir: lookup(configDir),
	}
}

func TestParseFlagsDefaults(t *testing.T) {
	home := t.TempDir()
	relocated := t.TempDir()
	elsewhere := t.TempDir()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		args      []string
		env       environment
		wantRoot  string
		wantLabel string
	}{
		{
			name:      "home directory",
			env:       testEnvironment(home, "", ""),
			wantRoot:  filepath.Join(home, defaultConfigDir, projectsDir),
			wantLabel: filepath.Join(homeLabel, defaultConfigDir, projectsDir),
		},
		{
			name:      "relocated configuration directory",
			env:       testEnvironment(home, relocated, ""),
			wantRoot:  filepath.Join(relocated, projectsDir),
			wantLabel: filepath.Join(relocated, projectsDir),
		},
		{
			name:      "relocated configuration directory without a home",
			env:       testEnvironment("", relocated, ""),
			wantRoot:  filepath.Join(relocated, projectsDir),
			wantLabel: filepath.Join(relocated, projectsDir),
		},
		{
			name:      "explicit root wins over both",
			args:      []string{"--root", elsewhere},
			env:       testEnvironment(home, relocated, ""),
			wantRoot:  elsewhere,
			wantLabel: elsewhere,
		},
		{
			name:      "explicit root under the home directory",
			args:      []string{"--root", filepath.Join(home, "work", "transcripts")},
			env:       testEnvironment(home, "", ""),
			wantRoot:  filepath.Join(home, "work", "transcripts"),
			wantLabel: filepath.Join(homeLabel, "work", "transcripts"),
		},
		{
			name:      "explicit root without a home",
			args:      []string{"--root", elsewhere},
			env:       testEnvironment("", "", ""),
			wantRoot:  elsewhere,
			wantLabel: elsewhere,
		},
		{
			name:      "relative root becomes absolute",
			args:      []string{"--root", "transcripts"},
			env:       testEnvironment(home, "", ""),
			wantRoot:  filepath.Join(workingDir, "transcripts"),
			wantLabel: filepath.Join(workingDir, "transcripts"),
		},
		{
			name:      "home directory itself",
			args:      []string{"--root", home},
			env:       testEnvironment(home, "", ""),
			wantRoot:  home,
			wantLabel: homeLabel,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseFlags(tt.args, tt.env)
			if err != nil {
				t.Fatalf("parseFlags: %v", err)
			}
			want := config{
				root:      tt.wantRoot,
				rootLabel: tt.wantLabel,
				days:      usage.DefaultWindowDays,
				sessions:  defaultSessions,
				timeout:   defaultTimeout,
			}
			if cfg != want {
				t.Errorf("config = %+v, want %+v", cfg, want)
			}
		})
	}
}

func TestParseFlagsValues(t *testing.T) {
	root := t.TempDir()
	dbDir := t.TempDir()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args []string
		want config
	}{
		{
			name: "every flag",
			args: []string{
				"--root", root, "--db", filepath.Join(dbDir, "usage.sqlite"), "--days", "7",
				"--project", "synthetic-project", "--sessions", "5", "--pretty", "--timeout", "90s",
			},
			want: config{
				root: root, rootLabel: root, dbPath: filepath.Join(dbDir, "usage.sqlite"), days: 7,
				project: "synthetic-project", sessions: 5, pretty: true, timeout: 90 * time.Second,
			},
		},
		{
			name: "limits and single-dash spelling",
			args: []string{"-root", root, "-days=365", "-sessions=200", "-db", "kept.sqlite"},
			want: config{
				root: root, rootLabel: root, dbPath: filepath.Join(workingDir, "kept.sqlite"),
				days: usage.MaxWindowDays, sessions: usage.MaxPageSize, timeout: defaultTimeout,
			},
		},
		{
			name: "lower limits",
			args: []string{"--root", root, "--days", "1", "--sessions", "1", "--timeout", "1ns"},
			want: config{root: root, rootLabel: root, days: 1, sessions: 1, timeout: time.Nanosecond},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseFlags(tt.args, testEnvironment("", "", ""))
			if err != nil {
				t.Fatalf("parseFlags: %v", err)
			}
			if cfg != tt.want {
				t.Errorf("config = %+v, want %+v", cfg, tt.want)
			}
		})
	}
}

// unusedDeps fails the test when the command gets past flag parsing.
func unusedDeps(t *testing.T, h *harness) deps {
	t.Helper()
	d := h.deps()
	d.open = func(context.Context, openParams) (usageService, func() error, error) {
		t.Error("pipeline opened despite invalid flags")
		return nil, nil, errInjected
	}
	return d
}

func TestExecuteRejectsBadFlags(t *testing.T) {
	root := t.TempDir()
	configDir := t.TempDir()
	appDir := filepath.Join(configDir, desktopDataDir)
	if err := os.Mkdir(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(appDir, linked); err != nil {
		t.Fatal(err)
	}
	withHome := testEnvironment(t.TempDir(), "", configDir)
	tests := []struct {
		name        string
		args        []string
		env         environment
		wantMessage string
	}{
		{name: "days below the range", args: []string{"--days", "0"}, env: withHome, wantMessage: "--days must be 1 to 365"},
		{name: "days above the range", args: []string{"--days", "366"}, env: withHome, wantMessage: "--days must be 1 to 365"},
		{name: "days negative", args: []string{"--days", "-3"}, env: withHome, wantMessage: "--days must be 1 to 365"},
		{name: "days not a number", args: []string{"--days", "month"}, env: withHome, wantMessage: "invalid value"},
		{name: "sessions below the range", args: []string{"--sessions", "0"}, env: withHome, wantMessage: "--sessions must be 1 to 200"},
		{name: "sessions above the range", args: []string{"--sessions", "201"}, env: withHome, wantMessage: "--sessions must be 1 to 200"},
		{name: "sessions not a number", args: []string{"--sessions", "all"}, env: withHome, wantMessage: "invalid value"},
		{name: "timeout zero", args: []string{"--timeout", "0"}, env: withHome, wantMessage: "--timeout must be positive"},
		{name: "timeout negative", args: []string{"--timeout", "-1s"}, env: withHome, wantMessage: "--timeout must be positive"},
		{name: "timeout without a unit", args: []string{"--timeout", "10"}, env: withHome, wantMessage: "invalid value"},
		{name: "pretty with a bad value", args: []string{"--pretty=maybe"}, env: withHome, wantMessage: "invalid boolean value"},
		{name: "flag without a value", args: []string{"--root"}, env: withHome, wantMessage: "flag needs an argument"},
		{name: "unknown flag", args: []string{"--verbose"}, env: withHome, wantMessage: "flag provided but not defined"},
		{name: "positional argument", args: []string{"--days", "7", "extra"}, env: withHome, wantMessage: errUnexpectedArgs.Error() + ": extra"},
		{name: "no home and no root", env: testEnvironment("", "", configDir), wantMessage: errNoDefaultRoot.Error()},
		{
			name:        "database in the desktop data directory",
			args:        []string{"--root", root, "--db", filepath.Join(appDir, "usage.sqlite")},
			env:         withHome,
			wantMessage: errDesktopDB.Error(),
		},
		{
			name:        "database in a new folder of the desktop data directory",
			args:        []string{"--root", root, "--db", filepath.Join(appDir, "new", "usage.sqlite")},
			env:         withHome,
			wantMessage: errDesktopDB.Error(),
		},
		{
			name:        "database reached through a symbolic link",
			args:        []string{"--root", root, "--db", filepath.Join(linked, "usage.sqlite")},
			env:         withHome,
			wantMessage: errDesktopDB.Error(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, newFakeService())
			if code := execute(t.Context(), tt.args, tt.env, unusedDeps(t, h)); code != exitUsage {
				t.Fatalf("exit code = %d, want %d", code, exitUsage)
			}
			if h.stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", h.stdout.String())
			}
			stderr := h.stderr.String()
			if !strings.Contains(stderr, tt.wantMessage) || !strings.Contains(stderr, "--help") {
				t.Errorf("stderr = %q, want %q and a pointer to --help", stderr, tt.wantMessage)
			}
		})
	}
}

func TestParseFlagsAcceptsDatabaseOutsideDesktopData(t *testing.T) {
	root := t.TempDir()
	configDir := t.TempDir()
	tests := []struct {
		name   string
		dbPath string
		env    environment
	}{
		{name: "sibling of the desktop data directory", dbPath: filepath.Join(configDir, desktopDataDir+"-cli", "usage.sqlite"), env: testEnvironment("", "", configDir)},
		{name: "another directory", dbPath: filepath.Join(t.TempDir(), "usage.sqlite"), env: testEnvironment("", "", configDir)},
		{name: "no configuration directory", dbPath: filepath.Join(t.TempDir(), "usage.sqlite"), env: testEnvironment("", "", "")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseFlags([]string{"--root", root, "--db", tt.dbPath}, tt.env)
			if err != nil {
				t.Fatalf("parseFlags: %v", err)
			}
			if cfg.dbPath != tt.dbPath {
				t.Errorf("database path = %s, want %s", cfg.dbPath, tt.dbPath)
			}
		})
	}
}

// writeFile creates a file with some content and returns its path.
func writeFile(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The desktop data directory and database are compared by identity, so that
// another name for them does not get through.
func TestExecuteRefusesDesktopDatabaseUnderAnotherName(t *testing.T) {
	root := t.TempDir()
	configDir := t.TempDir()
	appDir := filepath.Join(configDir, desktopDataDir)
	if err := os.Mkdir(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	appDB := writeFile(t, filepath.Join(appDir, desktopDatabaseFile))
	elsewhere := t.TempDir()
	linkedConfig := filepath.Join(elsewhere, "config-link")
	if err := os.Symlink(configDir, linkedConfig); err != nil {
		t.Fatal(err)
	}
	hardLinkToApp := filepath.Join(elsewhere, "hard-link.sqlite")
	if err := os.Link(appDB, hardLinkToApp); err != nil {
		t.Fatal(err)
	}
	symlinkToApp := filepath.Join(elsewhere, "symlink.sqlite")
	if err := os.Symlink(appDB, symlinkToApp); err != nil {
		t.Fatal(err)
	}
	other := writeFile(t, filepath.Join(elsewhere, "other.sqlite"))
	otherLink := filepath.Join(elsewhere, "other-link.sqlite")
	if err := os.Link(other, otherLink); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(elsewhere, "dangling.sqlite")
	if err := os.Symlink(filepath.Join(appDir, "new.sqlite"), dangling); err != nil {
		t.Fatal(err)
	}
	withHome := testEnvironment(t.TempDir(), "", configDir)
	tests := []struct {
		name    string
		dbPath  func(t *testing.T) string
		wantErr error
	}{
		{
			name: "different spelling on a case-insensitive file system",
			dbPath: func(t *testing.T) string {
				upper := filepath.Join(configDir, strings.ToUpper(desktopDataDir))
				same, err := os.Stat(upper)
				appInfo, appErr := os.Stat(appDir)
				if err != nil || appErr != nil || !os.SameFile(same, appInfo) {
					t.Skip("the temporary file system is case-sensitive, so another spelling names another directory")
				}
				return filepath.Join(upper, desktopDatabaseFile)
			},
			wantErr: errDesktopDB,
		},
		{
			name:    "symbolic link to a parent of the data directory",
			dbPath:  func(*testing.T) string { return filepath.Join(linkedConfig, desktopDataDir, "new.sqlite") },
			wantErr: errDesktopDB,
		},
		{
			name:    "hard link to the desktop database",
			dbPath:  func(*testing.T) string { return hardLinkToApp },
			wantErr: errDesktopDB,
		},
		{
			name:    "symbolic link to the desktop database",
			dbPath:  func(*testing.T) string { return symlinkToApp },
			wantErr: errDesktopDB,
		},
		{
			name:    "file with a second hard link",
			dbPath:  func(*testing.T) string { return otherLink },
			wantErr: errLinkedDB,
		},
		{
			name:    "symbolic link to a missing file",
			dbPath:  func(*testing.T) string { return dangling },
			wantErr: errDanglingDB,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := tt.dbPath(t)
			if _, err := parseFlags([]string{"--root", root, "--db", dbPath}, withHome); !errors.Is(err, tt.wantErr) {
				t.Fatalf("parseFlags error = %v, want %v", err, tt.wantErr)
			}
			h := newHarness(t, newFakeService())
			if code := execute(t.Context(), []string{"--root", root, "--db", dbPath}, withHome, unusedDeps(t, h)); code != exitUsage {
				t.Fatalf("exit code = %d, want %d", code, exitUsage)
			}
			if stderr := h.stderr.String(); !strings.Contains(stderr, tt.wantErr.Error()) || !strings.Contains(stderr, "--help") {
				t.Errorf("stderr = %q, want %q and a pointer to --help", stderr, tt.wantErr)
			}
		})
	}
}

func TestParseFlagsAcceptsExistingDatabaseOutsideDesktopData(t *testing.T) {
	configDir := t.TempDir()
	appDir := filepath.Join(configDir, desktopDataDir)
	if err := os.Mkdir(appDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(appDir, desktopDatabaseFile))
	dbPath := writeFile(t, filepath.Join(t.TempDir(), "usage.sqlite"))
	cfg, err := parseFlags([]string{"--root", t.TempDir(), "--db", dbPath}, testEnvironment("", "", configDir))
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if cfg.dbPath != dbPath {
		t.Errorf("database path = %s, want %s", cfg.dbPath, dbPath)
	}
}

func TestExecuteHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			h := newHarness(t, newFakeService())
			if code := execute(t.Context(), []string{arg}, testEnvironment("", "", ""), unusedDeps(t, h)); code != exitOK {
				t.Fatalf("exit code = %d, want %d", code, exitOK)
			}
			if h.stderr.Len() != 0 {
				t.Errorf("stderr = %q, want nothing", h.stderr.String())
			}
			help := h.stdout.String()
			for _, want := range []string{
				"--" + flagRoot, "--" + flagDB, "--" + flagDays, "--" + flagProject,
				"--" + flagSessions, "--" + flagPretty, "--" + flagTimeout,
				"$" + envConfigDir,
				"recorded only in the command's own database",
				"never opens or modifies the desktop app's",
				"makes no network calls",
			} {
				if !strings.Contains(help, want) {
					t.Errorf("help does not mention %q:\n%s", want, help)
				}
			}
		})
	}
}

func TestExecuteReportsHelpWriteFailure(t *testing.T) {
	h := newHarness(t, newFakeService())
	d := unusedDeps(t, h)
	d.stdout = failingWriter{}
	if code := execute(t.Context(), []string{"--help"}, testEnvironment("", "", ""), d); code != exitFailure {
		t.Fatalf("exit code = %d, want %d", code, exitFailure)
	}
}

func TestExecuteRunsWithParsedFlags(t *testing.T) {
	root := t.TempDir()
	svc := newFakeService()
	h := newHarness(t, svc)

	code := execute(t.Context(), []string{"--root", root, "--days", "3", "--pretty"}, testEnvironment("", "", ""), h.deps())
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, h.stderr.String())
	}
	if len(h.opened) != 1 || h.opened[0].root != root || h.opened[0].days != 3 {
		t.Errorf("open params = %+v", h.opened)
	}
	if !strings.HasPrefix(h.stdout.String(), "{\n") {
		t.Errorf("stdout is not indented: %s", h.stdout.String())
	}
}
