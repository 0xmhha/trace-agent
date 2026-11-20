// Package scenario loads the synthetic acceptance fixtures into disposable databases.
// The native collection and service entry points never consume this fixture format.
package scenario

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

// SeedResult resolves the fixture labels to their public, opaque identifiers.
type SeedResult struct {
	Point  store.Point
	Source string
	IDs    map[string]string
}

func (s SeedResult) intern(label string) string {
	if label == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("trace-agent/synthetic/v1/" + label))
	id := hex.EncodeToString(sum[:])
	s.IDs[label] = id
	return id
}

// Seed replays synthetic fact observations into an empty database. A nonzero
// revision requests a specific final fixture revision (e.g. the IA's #120).
func Seed(ctx context.Context, db *store.Store, input io.Reader, revision int64) (SeedResult, error) {
	result := SeedResult{IDs: map[string]string{}}
	base, err := db.Point(ctx)
	if err != nil {
		return result, err
	}
	if base.Revision != 0 {
		return result, errors.New("fixture requires an empty database")
	}
	var facts []store.FactProjection
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		var f store.FactProjection
		if err := json.Unmarshal(scanner.Bytes(), &f); err != nil {
			return result, err
		}
		if f.Occurred == nil {
			return result, errors.New("synthetic seed requires a timed observation")
		}
		facts = append(facts, f)
	}
	if err := scanner.Err(); err != nil {
		return result, err
	}
	if len(facts) == 0 {
		return result, errors.New("empty fixture")
	}
	minimum := int64(len(facts)) + 1
	if revision == 0 {
		revision = minimum
	}
	if revision < minimum {
		return result, errors.New("requested revision cannot contain fixture")
	}
	adapter, err := collect.New(collect.Rollout, "synthetic", "acceptance", "1", bytes.Repeat([]byte{7}, 32), nil, func() time.Time { return time.UnixMilli(*facts[0].Occurred) })
	if err != nil {
		return result, err
	}
	for i := int64(0); i < revision-1; i++ {
		label := fmt.Sprintf("padding-%d", i)
		at := *facts[0].Occurred - 1
		if i < int64(len(facts)) {
			// The native evidence carries a digest of this exact input fact so the
			// support can be reproduced without storing any synthetic free text.
			encoded, err := json.Marshal(facts[i])
			if err != nil {
				return result, err
			}
			sum := sha256.Sum256(encoded)
			label = hex.EncodeToString(sum[:])
			at = *facts[i].Occurred
		}
		raw, err := json.Marshal(map[string]any{"timestamp": time.UnixMilli(at).UTC().Format(time.RFC3339Nano), "type": "response_item", "payload": map[string]any{"type": "message", "role": "system", "id": label}})
		if err != nil {
			return result, err
		}
		e, err := adapter.Project(collect.Position{Stream: "proof", Generation: "fixture", Offset: i + 1}, raw)
		if err != nil {
			return result, err
		}
		if err = db.Append(ctx, e); err != nil {
			return result, err
		}
		result.Source = e.Source
	}
	base, err = db.Point(ctx)
	if err != nil {
		return result, err
	}
	rs, err := db.Records(ctx, base)
	if err != nil {
		return result, err
	}
	if int64(len(rs)) != revision-1 {
		return result, errors.New("fixture evidence count mismatch")
	}
	for i := range facts {
		f := &facts[i]
		for _, field := range []*string{&f.ID, &f.Task, &f.Session, &f.Root, &f.Actor, &f.Target, &f.Tool, &f.Module, &f.Server, &f.Attempt, &f.Model, &f.Stream, &f.Clock} {
			*field = result.intern(*field)
		}
		for j := range f.Parents {
			f.Parents[j].From = result.intern(f.Parents[j].From)
		}
		f.Source = result.Source
		for j, raw := range f.Evidence {
			index, err := strconv.Atoi(raw)
			if err != nil || index < 1 || index > len(facts) {
				return result, errors.New("fixture evidence reference invalid")
			}
			f.Evidence[j] = strconv.FormatInt(rs[index-1].ID, 10)
		}
	}
	result.Point, err = db.PublishFacts(ctx, base, facts)
	return result, err
}
