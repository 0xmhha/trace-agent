package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"time"

	"github.com/0xmhha/trace-agent/internal/extension"
)

// signalBuffer is how many signals may wait to be forwarded. os/signal drops
// a signal when the channel is full.
const signalBuffer = 8

// stdinDrainGrace bounds how long Wait lingers for stdin that is still being
// copied to a command that has already exited. Without it, input that never
// reaches end of file would keep trace-emit alive after the command.
const stdinDrainGrace = 100 * time.Millisecond

// run executes the wrapped command, reports it as a span and returns the
// command's exit status. Nothing about tracing can change that status.
func (e environment) run(ctx context.Context, opts options, ev extension.WireEvent) int {
	stdin := e.stdin
	if opts.fromHook {
		raw, err := readHookInput(e.stdin)
		// The command gets back what was read from its stdin, followed by
		// the unread rest when the input did not end within the limit.
		if err == nil && len(raw) <= hookInputLimit {
			stdin = bytes.NewReader(raw)
		} else {
			stdin = io.MultiReader(bytes.NewReader(raw), e.stdin)
		}
		if err == nil {
			err = linkFromHook(&ev, raw)
		}
		if err != nil && opts.verbose {
			e.warn("hook input ignored: %v", err)
		}
	}

	start := e.now()
	code, err := e.runCommand(opts.command, stdin)
	elapsed := e.now().Sub(start)
	if err != nil {
		e.warn("cannot run command: %v", err)
	}

	duration := milliseconds(elapsed)
	ev.Timestamp = formatTimestamp(start)
	ev.DurationMs = &duration
	ev.Status = string(statusOK)
	if code != exitOK {
		ev.Status = string(statusError)
	}
	if ev.Attributes == nil {
		ev.Attributes = make(map[string]any, 1)
	}
	ev.Attributes[attrExitCode] = code

	dest, err := resolveDestination(e.getenv)
	if err == nil {
		err = e.deliver(ctx, dest, ev)
	}
	if err != nil && opts.verbose {
		e.warn("event not sent: %v", err)
	}
	return code
}

// runCommand starts command with the caller's stdio, forwards signals to it
// and waits. It returns exitNotStarted and the reason when the command could
// not be started.
func (e environment) runCommand(command []string, stdin io.Reader) (int, error) {
	// No context: cancelling one would kill the command outright, while a
	// wrapper has to let it end on its own and hand signals on instead.
	cmd := exec.Command(command[0], command[1:]...) // #nosec G204 -- running the caller's command is the purpose of run mode.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, e.stdout, e.stderr
	cmd.WaitDelay = stdinDrainGrace

	// Subscribe before starting so that a signal arriving right after the
	// start reaches the command instead of ending trace-emit.
	signals, stop := e.signals()
	defer stop()
	if err := cmd.Start(); err != nil {
		return exitNotStarted, err
	}

	waited := make(chan struct{})
	// The goroutine ends when the command exits.
	go func() {
		defer close(waited)
		// The exit status is read from ProcessState below. Any other error
		// is about copying stdio and does not change what the command did.
		_ = cmd.Wait()
	}()
	for {
		select {
		case sig := <-signals:
			// Fails only when the command has already exited; then there is
			// nothing left to forward to.
			_ = cmd.Process.Signal(sig)
		case <-waited:
			return exitStatus(cmd.ProcessState), nil
		}
	}
}

// notifySignals subscribes to the signals that run mode forwards.
func notifySignals() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, signalBuffer)
	signal.Notify(ch, forwardedSignals()...)
	return ch, func() { signal.Stop(ch) }
}
