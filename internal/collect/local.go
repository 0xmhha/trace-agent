package collect

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

// CollectLocalCodex opens the installed CLI's default local profile over stdio.
// To attach to a different configured instance, pass its transport to CollectApp.
func (a *Adapter) CollectLocalCodex(ctx context.Context, opts AppOptions, sink AppSink) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--stdio")
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
