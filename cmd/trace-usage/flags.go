package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// program is the command name used in messages.
const program = "trace-usage"

// Flag names.
const (
	flagRoot     = "root"
	flagDB       = "db"
	flagDays     = "days"
	flagProject  = "project"
	flagSessions = "sessions"
	flagPretty   = "pretty"
	flagTimeout  = "timeout"
)

// Flag defaults and limits that the usage contract does not already name.
const (
	defaultSessions = 20
	minDays         = 1
	minSessions     = 1
	defaultTimeout  = 10 * time.Minute
)

// Where the default transcript root comes from.
const (
	// envConfigDir relocates the Claude Code configuration directory.
	envConfigDir     = "CLAUDE_CONFIG_DIR"
	defaultConfigDir = ".claude"
	projectsDir      = "projects"
	homeLabel        = "~"
)

// desktopDataDir is the desktop app's directory under the user configuration
// directory. The desktop module is a separate Go module, so the name is
// repeated here; it only backs the refusal of a --db path inside it.
const desktopDataDir = "org.0xmhha.trace-agent"

// desktopDatabaseFile is the desktop app's usage database in desktopDataDir,
// repeated from the desktop module for the same reason.
const desktopDatabaseFile = "usage.sqlite"

// Usage errors. Each of them ends the command with exitUsage.
var (
	errHelp           = flag.ErrHelp
	errUnexpectedArgs = errors.New("unexpected arguments")
	errFlagRange      = errors.New("flag value out of range")
	errNoDefaultRoot  = errors.New("cannot determine the default transcript root; pass --" + flagRoot)
	errDesktopDB      = errors.New("--" + flagDB + " must not point into the desktop app's data directory")
	errLinkedDB       = errors.New("--" + flagDB + " must not be a file with more than one hard link")
	errDanglingDB     = errors.New("--" + flagDB + " must not be a symbolic link to a missing file")
)

// environment is what the flag defaults read from the process.
type environment struct {
	getenv    func(string) string
	homeDir   func() (string, error)
	configDir func() (string, error)
}

// config is the validated command line. root is absolute and rootLabel its
// display form without the home directory. An empty dbPath selects a
// temporary database.
type config struct {
	root      string
	rootLabel string
	dbPath    string
	days      int
	project   string
	sessions  int
	pretty    bool
	timeout   time.Duration
}

// parseFlags validates args. It returns errHelp when help was requested.
func parseFlags(args []string, env environment) (config, error) {
	var cfg config
	fs := flag.NewFlagSet(program, flag.ContinueOnError)
	// Messages are printed by the caller, so that help goes to standard
	// output and errors to standard error.
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	fs.StringVar(&cfg.root, flagRoot, "", "")
	fs.StringVar(&cfg.dbPath, flagDB, "", "")
	fs.IntVar(&cfg.days, flagDays, usage.DefaultWindowDays, "")
	fs.StringVar(&cfg.project, flagProject, "", "")
	fs.IntVar(&cfg.sessions, flagSessions, defaultSessions, "")
	fs.BoolVar(&cfg.pretty, flagPretty, false, "")
	fs.DurationVar(&cfg.timeout, flagTimeout, defaultTimeout, "")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() > 0 {
		return config{}, fmt.Errorf("%w: %s", errUnexpectedArgs, strings.Join(fs.Args(), " "))
	}
	if cfg.days < minDays || cfg.days > usage.MaxWindowDays {
		return config{}, fmt.Errorf("%w: --%s must be %d to %d", errFlagRange, flagDays, minDays, usage.MaxWindowDays)
	}
	if cfg.sessions < minSessions || cfg.sessions > usage.MaxPageSize {
		return config{}, fmt.Errorf("%w: --%s must be %d to %d", errFlagRange, flagSessions, minSessions, usage.MaxPageSize)
	}
	if cfg.timeout <= 0 {
		return config{}, fmt.Errorf("%w: --%s must be positive", errFlagRange, flagTimeout)
	}
	if err := resolveRoot(&cfg, env); err != nil {
		return config{}, err
	}
	if err := resolveDB(&cfg, env); err != nil {
		return config{}, err
	}
	return cfg, nil
}

// resolveRoot fills the default root, makes it absolute and derives its
// label. The home directory is needed only for the default and the label, so
// an explicit root works without one.
func resolveRoot(cfg *config, env environment) error {
	home, homeErr := env.homeDir()
	if cfg.root == "" {
		switch configDir := env.getenv(envConfigDir); {
		case configDir != "":
			cfg.root = filepath.Join(configDir, projectsDir)
		case homeErr != nil || home == "":
			return errNoDefaultRoot
		default:
			cfg.root = filepath.Join(home, defaultConfigDir, projectsDir)
		}
	}
	abs, err := filepath.Abs(cfg.root)
	if err != nil {
		return fmt.Errorf("resolve --%s: %w", flagRoot, err)
	}
	cfg.root = abs
	cfg.rootLabel = abs
	if homeErr == nil && home != "" {
		if rel, ok := relativeInside(home, abs); ok {
			cfg.rootLabel = filepath.Join(homeLabel, rel)
		}
	}
	return nil
}

// resolveDB makes an explicit database path absolute and refuses one inside
// the desktop app's data directory: consent recorded there would bypass the
// app's own confirmation dialog.
func resolveDB(cfg *config, env environment) error {
	if cfg.dbPath == "" {
		return nil
	}
	abs, err := filepath.Abs(cfg.dbPath)
	if err != nil {
		return fmt.Errorf("resolve --%s: %w", flagDB, err)
	}
	cfg.dbPath = abs
	base, err := env.configDir()
	if err != nil || base == "" {
		// Without a configuration directory there is no desktop data
		// directory to protect.
		return nil
	}
	return refuseDesktopDB(filepath.Join(base, desktopDataDir), abs)
}

// refuseDesktopDB returns an error when dbPath, an absolute path, reaches the
// desktop app's data directory appDir or its database. Comparing path
// strings is not enough: a case-insensitive file system accepts another
// spelling of the same directory, and a hard link reaches the app's database
// from anywhere. So the directories and files are also compared by identity.
func refuseDesktopDB(appDir, dbPath string) error {
	// The lexical check also covers an app directory that does not exist
	// yet, which has no identity to compare.
	if _, inside := relativeInside(resolveExisting(appDir), resolveExisting(dbPath)); inside {
		return errDesktopDB
	}
	appInfo, err := os.Stat(appDir)
	if err != nil {
		// Nothing exists there yet that the database could reach.
		return nil
	}
	for dir := dbPath; ; {
		// A directory that cannot be examined is not the app's directory as
		// far as this check can tell; the app directory itself was examined.
		if info, err := os.Stat(dir); err == nil && os.SameFile(info, appInfo) {
			return errDesktopDB
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	dbInfo, err := os.Stat(dbPath)
	if err != nil {
		// Opening a symbolic link to a missing file creates the link's
		// target, which none of the checks above has seen.
		if linkInfo, lerr := os.Lstat(dbPath); lerr == nil && linkInfo.Mode()&os.ModeSymlink != 0 {
			return errDanglingDB
		}
		// A file that does not exist yet is created by this command and is
		// nobody else's database.
		return nil
	}
	if appDB, err := os.Stat(filepath.Join(appDir, desktopDatabaseFile)); err == nil && os.SameFile(dbInfo, appDB) {
		return errDesktopDB
	}
	// A second name for the file may lie inside the app directory, and the
	// other names of a file cannot be listed.
	if links, ok := linkCount(dbInfo); ok && links > 1 {
		return errLinkedDB
	}
	return nil
}

// relativeInside returns path relative to dir when path is dir itself or
// lies below it.
func relativeInside(dir, path string) (string, bool) {
	rel, err := filepath.Rel(dir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// resolveExisting resolves symbolic links in the longest existing prefix of
// path and re-attaches the rest, so that a file that does not exist yet still
// compares equal to its real location.
func resolveExisting(path string) string {
	rest := ""
	for dir := filepath.Clean(path); ; {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Clean(path)
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// helpText is the --help output.
func helpText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s [flags]\n\n", program)
	b.WriteString("Scans Claude Code transcripts, aggregates usage and prints one JSON object\n")
	fmt.Fprintf(&b, "with the keys %s to standard output.\n", strings.Join(reportKeys(), ", "))
	b.WriteString("Progress and errors go to standard error.\n\nFlags:\n")
	rows := [][2]string{
		{"--" + flagRoot + " DIR", fmt.Sprintf("transcript root (default $%s/%s, else %s)", envConfigDir, projectsDir, filepath.Join(homeLabel, defaultConfigDir, projectsDir))},
		{"--" + flagDB + " FILE", "usage database to create or reuse; it is kept (default: a temporary file, removed on exit)"},
		{"--" + flagDays + " N", fmt.Sprintf("collection window and report range in local calendar days, today included, %d to %d (default %d)", minDays, usage.MaxWindowDays, usage.DefaultWindowDays)},
		{"--" + flagProject + " KEY", "limit the report to one project key; the scan still covers every project (default: all)"},
		{"--" + flagSessions + " N", fmt.Sprintf("session rows to print, most recent first, %d to %d (default %d)", minSessions, usage.MaxPageSize, defaultSessions)},
		{"--" + flagPretty, "indent the JSON output"},
		{"--" + flagTimeout + " D", fmt.Sprintf("overall time limit (default %s)", defaultTimeout)},
	}
	for _, row := range rows {
		fmt.Fprintf(&b, "  %-16s %s\n", row[0], row[1])
	}
	b.WriteString("\nPrivacy:\n")
	b.WriteString("  Everything stays on this machine; the command makes no network calls.\n")
	b.WriteString("  Reading transcripts needs consent. Running this command grants it, and the\n")
	fmt.Fprintf(&b, "  consent is recorded only in the command's own database: the temporary file\n  or the --%s file. The command never opens or modifies the desktop app's\n", flagDB)
	fmt.Fprintf(&b, "  database and refuses a --%s path inside the desktop app's data directory.\n", flagDB)
	b.WriteString("  The database holds numbers and labels such as project paths and session\n  titles, never message text.\n")
	fmt.Fprintf(&b, "\nExit status: %d success, %d failure, %d invalid flags.\n", exitOK, exitFailure, exitUsage)
	return b.String()
}
