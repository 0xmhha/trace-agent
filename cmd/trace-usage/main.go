// Command trace-usage runs the usage pipeline without the desktop app and
// prints the aggregates as one JSON object. It is the tool for checking the
// pipeline against a real transcript root and a quick way to read usage
// numbers in a terminal.
//
// The command keeps its own database, a temporary file unless --db names
// one, and records the consent to read transcripts only there. It never
// opens the desktop app's database and makes no network calls.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage/db"
	"github.com/0xmhha/trace-agent/internal/usage/insight"
	"github.com/0xmhha/trace-agent/internal/usage/service"
	"github.com/0xmhha/trace-agent/internal/usage/timeline"
	"github.com/0xmhha/trace-agent/internal/usage/transcript"
)

// errRootNotDirectory reports a transcript root that is not a directory.
var errRootNotDirectory = errors.New("transcript root is not a directory")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// The first signal cancels the run and lets it clean up. Handling is then
	// handed back, so that a second signal ends a run that does not stop.
	context.AfterFunc(ctx, stop)
	code := execute(ctx, os.Args[1:], processEnvironment(), productionDeps(os.Stdout, os.Stderr))
	stop()
	os.Exit(code)
}

// execute parses args and runs the command. It returns the process exit code.
func execute(ctx context.Context, args []string, env environment, d deps) int {
	cfg, err := parseFlags(args, env)
	if errors.Is(err, errHelp) {
		if _, err = io.WriteString(d.stdout, helpText()); err != nil {
			return exitFailure
		}
		return exitOK
	}
	if err != nil {
		log := logger{w: d.stderr}
		log.printf("%v", err)
		log.printf("run '%s --help' for usage", program)
		return exitUsage
	}
	return run(ctx, cfg, d)
}

// processEnvironment reads flag defaults from the real process.
func processEnvironment() environment {
	return environment{getenv: os.Getenv, homeDir: os.UserHomeDir, configDir: os.UserConfigDir}
}

// productionDeps wires the real pipeline, clock and ticker.
func productionDeps(stdout, stderr io.Writer) deps {
	return deps{
		open:     openPipeline,
		now:      time.Now,
		location: time.Local,
		ticks:    tickEvery,
		stdout:   stdout,
		stderr:   stderr,
	}
}

// tickEvery is the wall-clock ticker behind the progress lines.
func tickEvery(interval time.Duration) (<-chan time.Time, func()) {
	ticker := time.NewTicker(interval)
	return ticker.C, ticker.Stop
}

// openPipeline opens the command's own usage store at p.dbPath, sets the
// collection window and builds the service over the real scanner, timeline
// reader and insight rules. There is no extension endpoint. The returned
// function closes the store.
func openPipeline(ctx context.Context, p openParams) (usageService, func() error, error) {
	// The root is checked before the database is created, so that a mistyped
	// path fails without leaving a file behind.
	info, err := os.Stat(p.root)
	if err != nil {
		return nil, nil, fmt.Errorf("open transcript root: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("open transcript root: %w", errRootNotDirectory)
	}
	store, err := db.Open(ctx, p.dbPath, p.now)
	if err != nil {
		return nil, nil, err
	}
	// The window is stored before the service exists, so that the first pass
	// already reads the requested number of days.
	if err = store.SetWindowDays(ctx, p.days); err != nil {
		return nil, nil, errors.Join(fmt.Errorf("set collection window: %w", err), store.Close())
	}
	svc, err := service.New(service.Config{
		Store:     store,
		Root:      p.root,
		RootLabel: p.rootLabel,
		Scan:      transcript.Scan,
		Timeline:  timeline.Read,
		Evaluate:  insight.Evaluate,
		Now:       p.now,
		Location:  p.location,
	})
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("build usage service: %w", err), store.Close())
	}
	return svc, store.Close, nil
}
