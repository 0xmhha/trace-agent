// Package transcript turns Claude Code transcript lines into usage records.
// It extracts numbers and names only and never returns message text.
package transcript

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// sniffBytes is how much of an oversized line is kept to find its type. User
// lines, which carry tool output and are the ones that grow, name their type
// within the first few hundred bytes. Assistant and attachment lines name it
// after their large member, so an oversized one of those goes uncounted.
const sniffBytes = 64 << 10

// titleMaskFactor bounds the title handed to usage.Mask as a multiple of the
// stored length. Masking the uncut text would let one long title stall a
// scan; cutting to the stored length first could split a secret so that its
// pattern no longer matches.
const titleMaskFactor = 4

// Scan reads complete JSONL lines from r, which is positioned at absolute
// file offset in.Base, until EOF or until the first line boundary at or after
// in.MaxBytes. A trailing line without a newline is left for the next call.
// Malformed lines never stop the scan: they are skipped and counted as
// described on usage.Batch. A line longer than usage.MaxLineBytes is skipped
// without being held in memory. Lines of another session are counted in
// Batch.Foreign when in.SessionID is set. Batch.BaseOffset is in.Base and
// Batch.NextOffset the absolute offset after the last complete line. An
// error is returned only when reading r fails.
func Scan(r io.Reader, in usage.ScanInput) (usage.Batch, error) {
	return scan(r, in, limits{line: usage.MaxLineBytes, sniff: sniffBytes})
}

// scan is Scan with injectable limits. When reading fails it still returns
// the batch of the complete lines read before the failure.
func scan(r io.Reader, in usage.ScanInput, lim limits) (usage.Batch, error) {
	s := scanner{in: in, session: in.SessionID, calls: map[string]int{}}
	reader := newLineReader(r, lim)
	var consumed int64
	for in.MaxBytes <= 0 || consumed < in.MaxBytes {
		ln, ok, err := reader.next()
		if err != nil {
			return s.finish(consumed), fmt.Errorf("read transcript: %w", err)
		}
		if !ok {
			break
		}
		consumed += ln.size
		s.batch.Lines++
		if ln.oversized {
			s.oversized(ln.data)
			continue
		}
		s.line(ln.data)
	}
	return s.finish(consumed), nil
}

// scanner accumulates the batch of one Scan call.
type scanner struct {
	in    usage.ScanInput
	batch usage.Batch
	// session is the session ID given to the records: the stream's when the
	// caller names it, otherwise the one on the latest line.
	session string
	// calls maps a message ID to its index in batch.ModelCalls.
	calls map[string]int
	// The latest non-empty session attributes, uncut until finish.
	cwd, rawTitle, entrypoint, version, gitBranch, agentType string
	first, last                                              time.Time
}

// line handles one complete line of acceptable length.
func (s *scanner) line(data []byte) {
	data = bytes.Trim(data, jsonSpace)
	if len(data) == 0 {
		return
	}
	var f fields
	if !walkObject(data, f.set) {
		s.unread(&f, true)
		return
	}
	if s.foreign(&f) {
		s.batch.Foreign++
		return
	}
	at := s.observe(&f)
	parse := parserOf(stringBody(f.kind))
	if parse != nil && !parse(s, &f, at) {
		s.batch.Skipped++
	}
}

// oversized handles the kept prefix of a line that was too long to parse.
func (s *scanner) oversized(prefix []byte) {
	var f fields
	walkObject(prefix, f.set)
	s.unread(&f, false)
}

// unread accounts for a line that could not be parsed, from the members that
// were readable before it broke off. A line of another session is foreign,
// not lost. A line of a kind the scanner does not read is no loss either.
// When the kind could not be read, countUnknown decides: a malformed line
// counts as skipped, because it may have been a record, while an oversized
// line counts only when its prefix proves that it was one.
func (s *scanner) unread(f *fields, countUnknown bool) {
	known := isString(f.kind)
	switch {
	case s.foreign(f):
		s.batch.Foreign++
	case known && parserOf(stringBody(f.kind)) != nil, !known && countUnknown:
		s.batch.Skipped++
	}
}

// foreign reports whether the line names a session other than the stream's.
// A line without a session ID belongs to the stream.
func (s *scanner) foreign(f *fields) bool {
	if s.in.SessionID == "" {
		return false
	}
	id := jsonString(f.sessionID)
	return id != "" && id != s.in.SessionID
}

// observe folds the session attributes and the time of a line into the batch
// and returns that time, zero when the line has none.
func (s *scanner) observe(f *fields) time.Time {
	if s.in.SessionID == "" {
		if id := jsonString(f.sessionID); id != "" {
			s.session = label(id)
		}
	}
	latest(&s.cwd, f.cwd)
	latest(&s.entrypoint, f.entrypoint)
	latest(&s.version, f.version)
	latest(&s.gitBranch, f.gitBranch)
	if s.in.AgentID != "" {
		latest(&s.agentType, f.agentType)
	}
	at := timestamp(f.timestamp)
	if at.IsZero() {
		return at
	}
	if s.first.IsZero() || at.Before(s.first) {
		s.first = at
	}
	if at.After(s.last) {
		s.last = at
	}
	return at
}

// finish completes the batch after consumed bytes of complete lines.
func (s *scanner) finish(consumed int64) usage.Batch {
	title, _ := usage.Mask(cutRunes(s.rawTitle, titleMaskFactor*usage.MaxLabelLen))
	s.batch.BaseOffset = s.in.Base
	s.batch.NextOffset = s.in.Base + consumed
	s.batch.Session = usage.SessionPatch{
		SessionID:     s.session,
		AgentID:       s.in.AgentID,
		AgentType:     label(s.agentType),
		ProjectPath:   label(s.cwd),
		Title:         label(title),
		Entrypoint:    label(s.entrypoint),
		ClientVersion: label(s.version),
		GitBranch:     label(s.gitBranch),
		FirstAt:       s.first,
		LastAt:        s.last,
	}
	return s.batch
}
