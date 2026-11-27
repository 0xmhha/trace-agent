package collect_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/readmodel"
	"github.com/0xmhha/trace-agent/internal/store"
)

type row = map[string]any

func query(t *testing.T, m *readmodel.Model, op string, args row) row {
	t.Helper()
	v, err := m.Query(op, args)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	out, _ := v.(row)
	return out
}

// A registered kind must pass every consumer of the registry outside this
// package: the writer stores it under its client, and the read model lists its
// source and evidence under the same codes.
func TestRegisteredKindsReachStoreAndReadModel(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "kinds.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	kinds := collect.Kinds()
	bySource := map[string]collect.Kind{}
	for _, k := range kinds {
		e := collect.SampleEvidence(t, k)
		if err := db.Append(ctx, e); err != nil {
			t.Fatalf("%s: the writer rejected a registered kind: %v", k, err)
		}
		if other, ok := bySource[e.Source]; ok {
			t.Fatalf("%s and %s share a source", k, other)
		}
		bySource[e.Source] = k
	}
	// Without a registry entry there is no client to store the source under.
	unknown := collect.SampleEvidence(t, collect.Transcript)
	unknown.Kind = "claude-unregistered"
	if err := db.Append(ctx, unknown); err == nil {
		t.Fatal("the writer stored an unregistered kind")
	}
	point, err := db.Point(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := db.Sources(ctx, point)
	if err != nil || len(sources) != len(kinds) {
		t.Fatalf("stored sources %d, registered kinds %d: %v", len(sources), len(kinds), err)
	}
	for _, s := range sources {
		k := bySource[s.ID]
		if s.Kind != string(k) || s.Client != collect.StoredClient(k) || !s.HasMetadata {
			t.Errorf("%s: stored as kind %q client %q metadata %t", k, s.Kind, s.Client, s.HasMetadata)
		}
	}
	records, err := db.Records(ctx, point)
	if err != nil || len(records) != len(kinds) {
		t.Fatalf("stored records %d, registered kinds %d: %v", len(records), len(kinds), err)
	}
	model := readmodel.New(records, readmodel.Options{})
	listed, _ := query(t, model, "sources", nil)["items"].([]row)
	if len(listed) != len(kinds) {
		t.Fatalf("read model sources %d, registered kinds %d", len(listed), len(kinds))
	}
	for _, s := range listed {
		id, _ := s["id"].(string)
		k := bySource[id]
		if s["kind"] != string(k) || s["client"] != collect.ClientCode(k) {
			t.Errorf("%s: read model lists kind %v client %v", k, s["kind"], s["client"])
		}
	}
	for _, r := range records {
		k := r.Evidence.Kind
		info, _ := collect.KindInfo(k)
		v := query(t, model, "evidence", row{"evidenceId": strconv.FormatInt(r.ID, 10)})
		detail, _ := v["evidence"].(row)
		if detail["sourceId"] != r.Evidence.Source || detail["observer"] != strings.ToUpper(info.Reporter) {
			t.Errorf("%s: evidence detail %v", k, detail)
		}
	}
}

// Evidence of a kind the read model has no decoder for must stay visible as
// unlinked. It cannot pass as matched evidence that produced nothing.
func TestUndecodedKindStaysUnlinked(t *testing.T) {
	e := collect.SampleEvidence(t, collect.Transcript)
	e.Kind = "claude-unregistered"
	model := readmodel.New([]store.Record{{ID: 1, Rev: 1, Evidence: e}}, readmodel.Options{})
	items, _ := query(t, model, "unlinked", row{"sourceId": e.Source})["items"].([]row)
	if len(items) != 1 || items[0]["link"] != "UNLINKED" {
		t.Fatalf("undecoded evidence is not listed as unlinked: %v", items)
	}
}
