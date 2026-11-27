package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// mode is which of the two command forms was invoked.
type mode int

const (
	modeEmit mode = iota
	modeRun
)

// runWord selects run mode when it is the first argument.
const runWord = "run"

// commandSeparator ends the flags of run mode and starts the command.
const commandSeparator = "--"

// Flag names. Error messages refer to them, so each is named once.
const (
	flagName         = "name"
	flagKind         = "kind"
	flagStatus       = "status"
	flagLevel        = "level"
	flagDurationMs   = "duration-ms"
	flagValue        = "value"
	flagUnit         = "unit"
	flagMessage      = "message"
	flagAttr         = "attr"
	flagSource       = "source"
	flagSessionID    = "session-id"
	flagToolUseID    = "tool-use-id"
	flagTraceID      = "trace-id"
	flagSpanID       = "span-id"
	flagParentSpanID = "parent-span-id"
	flagFromHook     = "from-hook"
	flagVerbose      = "verbose"
)

var (
	errUnexpectedArgument = errors.New("unexpected argument; flags only")
	errNoSeparator        = errors.New("run needs \"-- <command>\" after its flags")
	errNoCommand          = errors.New("run needs a command after \"--\"")
)

// options is the command line as typed. Every value stays a string here and
// is checked in buildEvent, so that no parse error can echo a flag value: a
// value may be sensitive and stderr may be captured.
type options struct {
	name         string
	kind         string
	status       string
	level        string
	durationMs   string
	value        string
	unit         string
	message      string
	source       string
	sessionID    string
	toolUseID    string
	traceID      string
	spanID       string
	parentSpanID string
	attrs        repeated
	fromHook     bool
	verbose      bool
	// command is the wrapped command of run mode.
	command []string
}

// repeated collects the values of a flag that may be given more than once.
type repeated []string

// String implements flag.Value.
func (r *repeated) String() string { return "" }

// Set implements flag.Value.
func (r *repeated) Set(value string) error {
	*r = append(*r, value)
	return nil
}

// parseOptions reads the flags of mode m. It returns flag.ErrHelp when help
// was asked for.
func parseOptions(m mode, args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet(programName, flag.ContinueOnError)
	// The flag package would print its own message and the full flag list.
	fs.SetOutput(io.Discard)
	for name, target := range map[string]*string{
		flagName:         &o.name,
		flagKind:         &o.kind,
		flagStatus:       &o.status,
		flagLevel:        &o.level,
		flagDurationMs:   &o.durationMs,
		flagValue:        &o.value,
		flagUnit:         &o.unit,
		flagMessage:      &o.message,
		flagSource:       &o.source,
		flagSessionID:    &o.sessionID,
		flagToolUseID:    &o.toolUseID,
		flagTraceID:      &o.traceID,
		flagSpanID:       &o.spanID,
		flagParentSpanID: &o.parentSpanID,
	} {
		fs.StringVar(target, name, "", "")
	}
	fs.Var(&o.attrs, flagAttr, "")
	fs.BoolVar(&o.fromHook, flagFromHook, false, "")
	fs.BoolVar(&o.verbose, flagVerbose, false, "")
	if err := fs.Parse(args); err != nil {
		return options{}, fmt.Errorf("parse flags: %w", err)
	}
	rest := fs.Args()
	if m == modeEmit {
		if len(rest) > 0 {
			return options{}, errUnexpectedArgument
		}
		return o, nil
	}
	// The flag package consumes the separator, so look at what preceded the
	// remaining arguments. Requiring it keeps a mistyped flag value from
	// being run as the command.
	separator := len(args) - len(rest) - 1
	if separator < 0 || args[separator] != commandSeparator {
		return options{}, errNoSeparator
	}
	if len(rest) == 0 {
		return options{}, errNoCommand
	}
	o.command = rest
	return o, nil
}
