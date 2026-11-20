package store

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/0xmhha/trace-agent/internal/collect"
)

func TestDeleteTargetsRequireConfirmedOwnership(t *testing.T) {
	ctx := t.Context()
	db := openStore(t, filepath.Join(t.TempDir(), "scope.sqlite"))
	a := adapter(t, collect.Transcript)
	var session, task string
	for i, raw := range []string{lineA, lineB, `{"type":"unknown","timestamp":"2023-11-14T22:13:20Z"}`} {
		e, err := a.Project(collect.Position{Stream: "file", Generation: "1", Offset: int64(i)}, []byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err = db.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			sess := e.Metadata["sessionId"].(string)
			session = collect.SessionKey(e.Profile, sess)
			task = collect.TaskKey(e.Profile, sess, e.Metadata["promptId"].(string))
		}
	}
	f := Filter{Source: a.Source(), Kind: "all", FromMS: 1699999999000, UntilMS: 1700000010000}
	ids, more, _, err := db.DeleteTargets(ctx, f, "SESSION", "")
	if err != nil || more || len(ids) != 1 || ids[0] != session {
		t.Fatal("session choices", ids, err)
	}
	ids, more, _, err = db.DeleteTargets(ctx, f, "TASK", "")
	if err != nil || more || len(ids) != 2 {
		t.Fatal("task choices", ids, err)
	}
	after, _, _, err := db.DeleteTargets(ctx, f, "TASK", ids[0])
	if err != nil || len(after) != 1 || after[0] != ids[1] {
		t.Fatal("page boundary", after, err)
	}
	f.Task = task
	p, err := db.Preview(ctx, f)
	if err != nil || p.Evidence != 2 {
		t.Fatal("expected task and unknown owner", p.Evidence, err)
	}
	for _, id := range []string{"malformed", strings.Repeat("f", 64)} {
		f.Task = id
		if _, err = db.Preview(ctx, f); !errors.Is(err, errInvalid) {
			t.Fatal("invalid preview target accepted", err)
		}
		if _, err = db.Delete(ctx, f, p); !errors.Is(err, errInvalid) {
			t.Fatal("invalid target reached deletion", err)
		}
	}
	f.Task = task
	f.Session = strings.Repeat("c", 64)
	if _, err = db.Preview(ctx, f); !errors.Is(err, errInvalid) {
		t.Fatal("mismatched session accepted", err)
	}
}
