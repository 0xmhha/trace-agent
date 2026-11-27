package usage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNonNil(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"overview", &Overview{}},
		{"tools", &Tools{}},
		{"projects", &Projects{}},
		{"session page", &SessionPage{Items: []SessionRow{{}}}},
		{"session detail", &SessionDetail{}},
		{"timeline page", &TimelinePage{}},
		{"insight report", &InsightReport{Items: []Insight{{}}}},
		{"ext overview", &ExtOverview{}},
		{"ext page", &ExtPage{}},
		{"status", &Status{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			NonNil(tc.value)
			raw, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			// Pointer-typed scalars such as costUsd legitimately encode as null;
			// a list never may.
			var decoded any
			if err = json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal(err)
			}
			if path := nullList(tc.value, raw); path != "" {
				t.Fatalf("%s encodes a nil list: %s", path, raw)
			}
		})
	}
	NonNil(nil)
	NonNil(Overview{})
}

// nullList reports a JSON key that is null although its Go field is a slice.
func nullList(_ any, raw []byte) string {
	for _, key := range []string{"reasons", "series", "models", "projects", "tools", "heatmap", "items", "mcpServers", "skills", "plugins", "subagents", "programs", "hooks", "agents", "turns", "extSources", "evidence", "sources", "excluded"} {
		if strings.Contains(string(raw), `"`+key+`":null`) {
			return key
		}
	}
	return ""
}
