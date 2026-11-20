package collect

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// CollectLocalCodex opens the installed CLI's default local profile over stdio.
// To attach to a different configured instance, pass its transport to CollectApp.
func (a *Adapter) CollectLocalCodex(ctx context.Context, opts AppOptions, sink AppSink) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	exe, err := exec.LookPath("codex")
	if err != nil && runtime.GOOS == "darwin" {
		// A Finder-launched process does not inherit the interactive shell's PATH.
		for _, candidate := range []string{"/opt/homebrew/bin/codex", "/usr/local/bin/codex", "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex"} {
			exe, err = exec.LookPath(candidate)
			if err == nil {
				break
			}
		}
	}
	if err != nil {
		return errors.New("cannot locate installed codex")
	}
	// #nosec G204 -- The executable is the local codex CLI; the arguments are constants.
	cmd := exec.CommandContext(ctx, exe, "app-server", "--stdio")
	cmd.WaitDelay = 2 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.New("cannot open app-server input")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return errors.New("cannot open app-server output")
	}
	pipe := &stdioConnection{ReadCloser: stdout, WriteCloser: stdin}
	if err := cmd.Start(); err != nil {
		_ = pipe.Close()
		return errors.New("cannot start app-server")
	}
	err = a.CollectApp(ctx, pipe, opts, sink)
	cancel()
	// Completion of a bounded read or cancellation deliberately stops the child.
	_ = cmd.Wait()
	return err
}

type stdioConnection struct {
	io.ReadCloser
	io.WriteCloser
	once sync.Once
	err  error
}

func (s *stdioConnection) Close() error {
	s.once.Do(func() { s.err = errors.Join(s.WriteCloser.Close(), s.ReadCloser.Close()) })
	return s.err
}
