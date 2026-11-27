package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/extension"
)

// signalBuffer is how many signals may wait to be forwarded. os/signal drops
// a signal when the channel is full.
const signalBuffer = 8

// stdinDrainGrace bounds how long Wait lingers for stdio that exec is still
// copying for a command that has already exited, for example output that a
// process the command left behind keeps open. Stdin that is not a file does
// not depend on it: stdinFeed copies that.
const stdinDrainGrace = 100 * time.Millisecond

// run executes the wrapped command, reports it as a span and returns the
// command's exit status. Nothing about tracing can change that status.
func (e environment) run(ctx context.Context, opts options, ev extension.WireEvent) int {
	stdin := e.stdin
	if opts.fromHook {
		// The wait is bounded like emit mode's: on a terminal or a pipe that
		// stays open the command starts unlinked instead of never.
		read := startHookRead(e.stdin)
		defer read.stop()
		hookCtx, cancel := context.WithTimeout(ctx, hookReadTimeout)
		raw, rest, err := read.wait(hookCtx)
		cancel()
		// The command gets back what was read from its stdin, followed by
		// whatever stdin still holds.
		stdin = io.MultiReader(bytes.NewReader(raw), rest)
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
	cmd.Env = childEnvironment(e.environ())
	cmd.WaitDelay = stdinDrainGrace

	// Input that is not a file is fed through a pipe of our own rather than
	// by exec. Wait waits for the copying goroutines exec starts, and a read
	// from input that never ends would hold it forever; this one is let go
	// when the command has exited.
	var feed *stdinFeed
	if _, isFile := stdin.(*os.File); stdin != nil && !isFile {
		var err error
		if feed, err = newStdinFeed(); err != nil {
			return exitNotStarted, err
		}
		defer feed.close()
		cmd.Stdin = feed.child
	}

	// Subscribe before starting so that a signal arriving right after the
	// start reaches the command instead of ending trace-emit.
	signals, stop := e.signals()
	defer stop()
	if err := cmd.Start(); err != nil {
		return exitNotStarted, err
	}
	if feed != nil {
		feed.start(stdin)
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

// stdinFeed copies input that is not a file into a pipe whose read end is
// the command's stdin.
type stdinFeed struct {
	child  *os.File // read end, handed to the command
	parent *os.File // write end, written by the feeding goroutine
}

func newStdinFeed() (*stdinFeed, error) {
	child, parent, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	return &stdinFeed{child: child, parent: parent}, nil
}

// start feeds src to the started command. The command holds its own copy of
// the read end, so ours is closed now; the command then sees end of file
// once the write end is closed.
func (f *stdinFeed) start(src io.Reader) {
	_ = f.child.Close()
	// The goroutine ends when src ends, or when a write fails because close
	// closed the write end. A read of src that blocks is released by its
	// owner: run stops the hook read after the command has run.
	go func() {
		// A failed write means the command stopped reading; that is its
		// business and changes nothing about its exit status.
		_, _ = io.Copy(f.parent, src)
		_ = f.parent.Close()
	}()
}

// close releases both ends. Errors are about ends already closed.
func (f *stdinFeed) close() {
	_ = f.child.Close()
	_ = f.parent.Close()
}

// childEnvironment returns parent without the credential. The wrapped command
// and everything it starts, such as build scripts and package install hooks,
// have no use for it, and any of them could send it elsewhere. The endpoint
// address stays so that a nested trace-emit still knows where events would
// go. The result is never nil: a nil environment would make exec inherit the
// parent's, credential included.
func childEnvironment(parent []string) []string {
	child := make([]string, 0, len(parent))
	for _, entry := range parent {
		name, _, _ := strings.Cut(entry, "=")
		if sameEnvName(name, envToken) {
			continue
		}
		child = append(child, entry)
	}
	return child
}

// notifySignals subscribes to the signals that run mode forwards.
func notifySignals() (<-chan os.Signal, func()) {
	ch := make(chan os.Signal, signalBuffer)
	signal.Notify(ch, forwardedSignals()...)
	return ch, func() { signal.Stop(ch) }
}
