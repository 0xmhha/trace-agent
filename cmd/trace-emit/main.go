// Command trace-emit sends one extension event to the local Trace Agent
// endpoint, or runs a command and reports it as a span.
//
// Tracing is optional for the caller. A missing or refused endpoint, a failed
// send and a rejected event never change the exit status; only a command line
// that cannot be understood does, with status 2. In run mode the exit status
// is the wrapped command's.
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/extension"
)

const programName = "trace-emit"

// Exit statuses of the command itself. In run mode every other status comes
// from the wrapped command.
const (
	exitOK         = 0
	exitFailure    = 1
	exitUsage      = 2
	exitNotStarted = 127
)

// Environment variables the command reads.
const (
	envURL    = "TRACE_EVENTS_URL"
	envToken  = "TRACE_EVENTS_TOKEN" // #nosec G101 -- the name of a variable, not a credential.
	envSource = "TRACE_EVENTS_SOURCE"
)

const redacted = "[redacted]"

const usageText = `Usage:
  trace-emit [flags]                           send one event
  trace-emit run [flags] -- <command> [args]   run a command and send a span

Flags:
  --name string             event name (required; run defaults to the command name)
  --kind string             span, log or metric (default log; run defaults to span)
  --status string           ok, error or unknown (run sets it from the exit status)
  --level string            debug, info, warn or error
  --duration-ms number      duration in milliseconds (run measures it)
  --value number            metric value
  --unit string             unit of the value
  --message string          free text
  --attr key=value          attribute; repeat for more
  --source string           sender name (default $TRACE_EVENTS_SOURCE, else trace-emit)
  --session-id string       Claude Code session to link the event to
  --tool-use-id string      tool call to link the event to
  --trace-id string         trace identifier
  --span-id string          span identifier
  --parent-span-id string   parent span identifier
  --from-hook               read session_id and tool_use_id from hook JSON on stdin
  --verbose                 print to stderr why an event was not sent

The endpoint and credential come from TRACE_EVENTS_URL and TRACE_EVENTS_TOKEN.
Sending never fails the caller: without them, or when the send fails, the exit
status is still 0 (run: the command's status). A malformed flag exits 2.
`

// environment is everything the command takes from its process, injected so
// that tests can replace the clock, the randomness, the transport and stdio.
type environment struct {
	getenv func(string) string
	// environ lists the process environment as key=value pairs; run mode
	// builds the wrapped command's environment from it.
	environ   func() []string
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	now       func() time.Time
	random    io.Reader
	transport http.RoundTripper
	// signals subscribes to the signals that run mode forwards and returns
	// the function that ends the subscription.
	signals func() (<-chan os.Signal, func())
}

func main() {
	env := environment{
		getenv:  os.Getenv,
		environ: os.Environ,
		stdin:   os.Stdin,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		now:     time.Now,
		random:  rand.Reader,
		// No proxy: the event and the credential must not leave this machine.
		transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
		signals:   notifySignals,
	}
	os.Exit(env.dispatch(context.Background(), os.Args[1:]))
}

// dispatch runs the command line args and returns the exit status.
func (e environment) dispatch(ctx context.Context, args []string) int {
	m := modeEmit
	if len(args) > 0 && args[0] == runWord {
		m, args = modeRun, args[1:]
	}
	opts, err := parseOptions(m, args)
	if errors.Is(err, flag.ErrHelp) {
		_, _ = io.WriteString(e.stdout, usageText)
		return exitOK
	}
	if err != nil {
		return e.usageError(err)
	}
	ev, err := buildEvent(m, opts)
	if err != nil {
		return e.usageError(err)
	}
	if m == modeRun {
		return e.run(ctx, opts, ev)
	}
	if err := e.emit(ctx, opts, ev); err != nil && opts.verbose {
		e.warn("event not sent: %v", err)
	}
	return exitOK
}

// emit sends ev once. The endpoint is resolved before stdin is read so that
// a caller without tracing returns at once.
func (e environment) emit(ctx context.Context, opts options, ev extension.WireEvent) error {
	dest, err := resolveDestination(e.getenv)
	if err != nil {
		return err
	}
	if opts.fromHook {
		hookCtx, cancel := context.WithTimeout(ctx, hookReadTimeout)
		raw, err := readHookInputWithin(hookCtx, e.stdin)
		cancel()
		if err == nil {
			err = linkFromHook(&ev, raw)
		}
		if err != nil && opts.verbose {
			e.warn("hook input ignored: %v", err)
		}
	}
	ev.Timestamp = formatTimestamp(e.now())
	return e.deliver(ctx, dest, ev)
}

// usageError reports a command line that cannot be understood. It is printed
// without --verbose because it is the caller's mistake, not a tracing outage.
func (e environment) usageError(err error) int {
	e.warn("%v (see %s --help)", err, programName)
	return exitUsage
}

// warn writes one diagnostic line to stderr. The credential is removed from
// the text so that no error path can print it.
func (e environment) warn(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if token := e.getenv(envToken); token != "" {
		text = strings.ReplaceAll(text, token, redacted)
	}
	_, _ = fmt.Fprintf(e.stderr, "%s: %s\n", programName, text)
}
