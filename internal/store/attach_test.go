package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/0xmhha/trace-agent/internal/collect"
)

func TestAtomicAttach(t *testing.T) {
	ctx := t.Context()
	db := openStore(t, filepath.Join(t.TempDir(), "trace.db"))
	a := adapter(t, collect.Transcript)
	registered := func(want bool) {
		exists, err := db.Registered(ctx, a.Source())
		if err != nil || exists != want {
			t.Fatal("registered", exists, err)
		}
	}
	pointIs := func(want Point) {
		got, err := db.Point(ctx)
		if err != nil || got != want {
			t.Fatal("point", got, err)
		}
	}
	exec := func(query string) {
		if _, err := db.db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	registered(false)
	from, until := int64(0), int64(1900000000000)
	exec("CREATE TRIGGER fail_new_source BEFORE INSERT ON source_config BEGIN SELECT RAISE(ABORT,'synthetic configuration failure'); END")
	if _, err := db.Attach(ctx, a, 30, &from, &until); err == nil {
		t.Fatal("partial source registration")
	}
	registered(false)
	pointIs(Point{Revision: 0, Epoch: 1})
	exec("DROP TRIGGER fail_new_source")
	first, err := db.Attach(ctx, a, 30, &from, &until)
	if err != nil || first.Revision != 1 {
		t.Fatal(first, err)
	}
	registered(true)
	if same, err := db.Register(ctx, a); err != nil || same != first {
		t.Fatal("register changed settings", same, err)
	}
	dir := t.TempDir()
	if err = os.WriteFile(filepath.Join(dir, "trace.jsonl"), []byte(lineA+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cursor := ingestFile(t, db, dir, false)
	latest, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	next := until + 1000
	exec("CREATE TRIGGER fail_history_reset BEFORE DELETE ON checkpoint BEGIN SELECT RAISE(ABORT,'synthetic checkpoint failure'); END")
	if _, err = db.Attach(ctx, a, 45, &from, &next); err == nil {
		t.Fatal("partial policy update")
	}
	pointIs(latest)
	cfg, err := db.SourceConfig(ctx, a.Source())
	if err != nil || cfg.RetentionDays != 30 || *cfg.UntilMS != until {
		t.Fatal(cfg, err)
	}
	c, err := db.Cursor(ctx, a.Source(), cursor.Stream)
	if err != nil || c != cursor {
		t.Fatal("lost cursor on rollback", c, err)
	}
	exec("DROP TRIGGER fail_history_reset")
	p, err := db.Attach(ctx, a, 0, &from, &next)
	if err != nil || p.Revision != latest.Revision+1 {
		t.Fatal(p, err)
	}
	c, err = db.Cursor(ctx, a.Source(), cursor.Stream)
	if err != nil || c.Offset != 0 {
		t.Fatal("history checkpoint not reset", c, err)
	}
	same, err := db.Attach(ctx, a, 0, nil, nil)
	if err != nil || same != p {
		t.Fatal("retry created revision", same, err)
	}
	cfg, err = db.SourceConfig(ctx, a.Source())
	if err != nil || cfg.RetentionDays != 30 || *cfg.UntilMS != next {
		t.Fatal(cfg, err)
	}
}
