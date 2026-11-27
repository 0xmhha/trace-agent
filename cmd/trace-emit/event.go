package main

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/extension"
)

// eventKind is the kind field of the wire format.
type eventKind string

const (
	kindSpan   eventKind = "span"
	kindLog    eventKind = "log"
	kindMetric eventKind = "metric"
)

// eventStatus is the status field of the wire format.
type eventStatus string

const (
	statusOK      eventStatus = "ok"
	statusError   eventStatus = "error"
	statusUnknown eventStatus = "unknown"
)

// eventLevel is the level field of the wire format.
type eventLevel string

const (
	levelDebug eventLevel = "debug"
	levelInfo  eventLevel = "info"
	levelWarn  eventLevel = "warn"
	levelError eventLevel = "error"
)

func eventKinds() []eventKind { return []eventKind{kindSpan, kindLog, kindMetric} }

func eventStatuses() []eventStatus { return []eventStatus{statusOK, statusError, statusUnknown} }

func eventLevels() []eventLevel {
	return []eventLevel{levelDebug, levelInfo, levelWarn, levelError}
}

// defaultKind is the kind sent when --kind is absent.
func (m mode) defaultKind() eventKind {
	if m == modeRun {
		return kindSpan
	}
	return kindLog
}

// defaultSource names the sender when neither the flag nor the environment
// does.
const defaultSource = programName

// attrExitCode is the attribute run mode adds to its span.
const attrExitCode = "exit_code"

// attrSeparator splits an --attr argument into key and value.
const attrSeparator = "="

// sentHeadroom is how much longer than the stored limit a free-text field
// may be when it leaves this command. The endpoint masks secrets and then
// cuts the text at its limit; cutting here at exactly that limit could split
// a secret so that its pattern no longer matches. The bound still keeps the
// largest possible body below extension.MaxBodyBytes.
const sentHeadroom = 4

const (
	maxSentMessage   = sentHeadroom * extension.MaxMessageLen
	maxSentAttribute = sentHeadroom * extension.MaxAttributeValue
)

// timestampLayout is RFC 3339 with milliseconds.
const timestampLayout = "2006-01-02T15:04:05.000Z07:00"

var errEnvSource = errors.New(envSource + " is not a valid source name")

// buildEvent turns the flags of mode m into an event and checks each value
// against the wire format. Every error it returns is a usage error. The
// timestamp, the identifier and, when --source is absent, the source are
// filled in later.
func buildEvent(m mode, o options) (extension.WireEvent, error) {
	ev := extension.WireEvent{
		Version: extension.WireVersion,
		Kind:    cmp.Or(o.kind, string(m.defaultKind())),
		Status:  o.status,
		Level:   o.level,
		Source:  o.source,
		Unit:    o.unit,
		Message: truncateRunes(o.message, maxSentMessage),
	}
	ev.Name = o.name
	if ev.Name == "" && m == modeRun {
		ev.Name = truncateRunes(filepath.Base(o.command[0]), extension.MaxNameLen)
	}
	switch {
	case ev.Name == "":
		return ev, flagError(flagName, "is required")
	case utf8.RuneCountInString(ev.Name) > extension.MaxNameLen:
		return ev, flagError(flagName, fmt.Sprintf("is longer than %d characters", extension.MaxNameLen))
	case !isOneOf(ev.Kind, eventKinds()):
		return ev, flagError(flagKind, "must be one of "+joined(eventKinds()))
	case ev.Status != "" && !isOneOf(ev.Status, eventStatuses()):
		return ev, flagError(flagStatus, "must be one of "+joined(eventStatuses()))
	case ev.Level != "" && !isOneOf(ev.Level, eventLevels()):
		return ev, flagError(flagLevel, "must be one of "+joined(eventLevels()))
	case ev.Source != "" && !extension.SourcePattern.MatchString(ev.Source):
		return ev, flagError(flagSource, "must match "+extension.SourcePattern.String())
	case utf8.RuneCountInString(ev.Unit) > extension.MaxUnitLen:
		return ev, flagError(flagUnit, fmt.Sprintf("is longer than %d characters", extension.MaxUnitLen))
	}
	if m == modeRun {
		// Run mode measures these itself; accepting them would send a span
		// that contradicts what happened.
		for _, measured := range []struct{ flag, value string }{
			{flagStatus, o.status},
			{flagDurationMs, o.durationMs},
		} {
			if measured.value != "" {
				return ev, flagError(measured.flag, "is set by run from the command")
			}
		}
	}
	for _, id := range []struct {
		flag  string
		value string
		field *string
	}{
		{flagSessionID, o.sessionID, &ev.SessionID},
		{flagToolUseID, o.toolUseID, &ev.ToolUseID},
		{flagTraceID, o.traceID, &ev.TraceID},
		{flagSpanID, o.spanID, &ev.SpanID},
		{flagParentSpanID, o.parentSpanID, &ev.ParentSpanID},
	} {
		if id.value != "" && !extension.IDPattern.MatchString(id.value) {
			return ev, flagError(id.flag, "must match "+extension.IDPattern.String())
		}
		*id.field = id.value
	}
	var err error
	if ev.DurationMs, err = parseAmount(flagDurationMs, o.durationMs); err != nil {
		return ev, err
	}
	if ev.Value, err = parseAmount(flagValue, o.value); err != nil {
		return ev, err
	}
	if ev.Attributes, err = parseAttributes(m, o.attrs); err != nil {
		return ev, err
	}
	return ev, nil
}

// parseAmount reads an optional number that the wire format requires to be
// finite and not negative.
func parseAmount(name, raw string) (*float64, error) {
	if raw == "" {
		return nil, nil
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return nil, flagError(name, "must be a finite number that is not negative")
	}
	return &n, nil
}

// parseAttributes reads the --attr arguments. A repeated key keeps its last
// value. Values are sent as strings, which is how the endpoint stores them.
func parseAttributes(m mode, pairs []string) (map[string]any, error) {
	limit := extension.MaxAttributes
	if m == modeRun {
		limit-- // run adds exit_code
	}
	var attrs map[string]any
	for _, pair := range pairs {
		key, value, found := strings.Cut(pair, attrSeparator)
		switch {
		case !found:
			return nil, flagError(flagAttr, "needs key"+attrSeparator+"value")
		case !extension.AttributeKeyPattern.MatchString(key):
			return nil, flagError(flagAttr, "key must match "+extension.AttributeKeyPattern.String())
		case m == modeRun && key == attrExitCode:
			return nil, flagError(flagAttr, "key "+attrExitCode+" is set by run from the command")
		}
		if attrs == nil {
			attrs = make(map[string]any, len(pairs))
		}
		attrs[key] = truncateRunes(value, maxSentAttribute)
	}
	if len(attrs) > limit {
		return nil, flagError(flagAttr, fmt.Sprintf("allows at most %d attributes", limit))
	}
	return attrs, nil
}

// resolveSource returns the source of an event whose --source flag gave
// fromFlag. An invalid name in the environment is not a usage error: the
// environment is not the command line, so the event is skipped instead.
func resolveSource(fromFlag string, getenv func(string) string) (string, error) {
	if fromFlag != "" {
		return fromFlag, nil
	}
	fromEnv := getenv(envSource)
	if fromEnv == "" {
		return defaultSource, nil
	}
	if !extension.SourcePattern.MatchString(fromEnv) {
		return "", errEnvSource
	}
	return fromEnv, nil
}

// formatTimestamp renders t the way the wire format expects.
func formatTimestamp(t time.Time) string { return t.UTC().Format(timestampLayout) }

// milliseconds converts d to the unit of duration_ms. A clock that stepped
// backwards yields zero, because the wire format refuses negative numbers.
func milliseconds(d time.Duration) float64 {
	return max(0, float64(d)/float64(time.Millisecond))
}

// flagError describes a problem with one flag. It never includes the flag's
// value.
func flagError(name, problem string) error {
	return fmt.Errorf("--%s %s", name, problem)
}

// truncateRunes cuts s to at most limit characters without splitting one.
func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	count := 0
	for i := range s {
		if count == limit {
			return s[:i]
		}
		count++
	}
	return s
}

func isOneOf[T ~string](value string, allowed []T) bool {
	return slices.Contains(allowed, T(value))
}

func joined[T ~string](allowed []T) string {
	parts := make([]string, len(allowed))
	for i, a := range allowed {
		parts[i] = string(a)
	}
	return strings.Join(parts, ", ")
}
