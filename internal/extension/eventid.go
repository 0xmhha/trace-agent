package extension

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// eventIDBytes is how much of a digest, or how many random bytes, an event ID
// keeps; the ID is its hex form.
const eventIDBytes = 16

// Domain tags keep the two derived ID kinds apart, so that a sender-chosen
// key can never be made to collide with a content hash.
const (
	idDomainSenderKey = "ext-event/sender-key/v1"
	idDomainContent   = "ext-event/content/v1"
)

// Markers of an optional number in the canonical encoding.
const (
	canonAbsent  = "-"
	canonPresent = "+"
)

// eventID derives the stored ID of a validated, masked event.
//
//   - With a sender key it depends on the source and that key only, so a
//     re-sent event is stored once whatever else changed.
//   - Without a key but with a sender timestamp it is a hash of the stored
//     content, so the same content at the same instant is one event.
//   - With neither it is random, so every such event is stored as new. The
//     receive time is deliberately no substitute for the missing timestamp:
//     it differs on every delivery and would identify nothing.
func eventID(senderKey string, timestamped bool, e *usage.ExtEvent, random io.Reader) (string, error) {
	switch {
	case senderKey != "":
		c := newCanon(idDomainSenderKey)
		c.text(e.Source)
		c.text(senderKey)
		return c.id(), nil
	case timestamped:
		return contentID(e), nil
	}
	var raw [eventIDBytes]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return "", fmt.Errorf("extension: random event id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// contentID hashes every stored field except ID and ReceivedAt in a fixed
// order. Attributes are written in key order so that map iteration order
// never reaches the hash.
func contentID(e *usage.ExtEvent) string {
	c := newCanon(idDomainContent)
	c.text(e.Source)
	c.text(string(e.Kind))
	c.text(e.Name)
	c.integer(e.At.Unix())
	c.integer(int64(e.At.Nanosecond()))
	c.number(e.DurationMs)
	c.text(string(e.Status))
	c.text(string(e.Level))
	c.text(e.SessionID)
	c.text(e.ToolUseID)
	c.text(e.TraceID)
	c.text(e.SpanID)
	c.text(e.ParentSpanID)
	c.text(e.Message)
	c.integer(int64(len(e.Attributes)))
	for _, key := range slices.Sorted(maps.Keys(e.Attributes)) {
		c.text(key)
		c.text(e.Attributes[key])
	}
	c.number(e.Value)
	c.text(e.Unit)
	return c.id()
}

// canon writes fields into a hash as length-prefixed strings, which makes
// the encoding unambiguous: no two field sequences share a byte stream.
type canon struct {
	sum hash.Hash
}

func newCanon(domain string) *canon {
	c := &canon{sum: sha256.New()}
	c.text(domain)
	return c
}

func (c *canon) text(s string) {
	// hash.Hash documents that Write never returns an error.
	_, _ = io.WriteString(c.sum, strconv.Itoa(len(s)))
	_, _ = io.WriteString(c.sum, ":")
	_, _ = io.WriteString(c.sum, s)
}

func (c *canon) integer(n int64) {
	c.text(strconv.FormatInt(n, 10))
}

// number writes the exact bits of an optional float; a formatted decimal
// could merge two different values.
func (c *canon) number(v *float64) {
	if v == nil {
		c.text(canonAbsent)
		return
	}
	c.text(canonPresent + strconv.FormatUint(math.Float64bits(*v), 16))
}

func (c *canon) id() string {
	return hex.EncodeToString(c.sum.Sum(nil)[:eventIDBytes])
}
