package main

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/scenario"
	"github.com/0xmhha/trace-agent/internal/store"
)

func seedDeletion(ctx context.Context, t *testing.T, db *store.Store) scenario.SeedResult {
	t.Helper()
	a, err := collect.New(collect.Transcript, "test", "delete", "2.1.329", make([]byte, 32), nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Register(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err = db.SetConsent(ctx, a.Source(), true); err != nil {
		t.Fatal(err)
	}
	a = a.WithBodyCollection()
	ids := map[string]string{}
	for i, owner := range []string{"one", "one", "two"} {
		r := map[string]any{"type": "user", "uuid": strconv.Itoa(i), "sessionId": owner, "promptId": strconv.Itoa(i), "userType": "external", "isMeta": false, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "message": map[string]any{"role": "user", "content": "sample text"}}
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		e, err := a.Project(collect.Position{Stream: "fixture", Generation: "1", Offset: int64(i)}, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
		session := e.Metadata["sessionId"].(string)
		ids[owner] = collect.SessionKey(e.Profile, session)
		ids["task"+strconv.Itoa(i)] = collect.TaskKey(e.Profile, session, e.Metadata["promptId"].(string))
	}
	p, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return scenario.SeedResult{Source: a.Source(), Point: p, IDs: ids}
}
