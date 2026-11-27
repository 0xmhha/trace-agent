package observer

import (
	"strings"
	"testing"

	"github.com/0xmhha/trace-agent/internal/collect"
)

// Every observer kind needs its own endpoint; a kind that is not an observer
// has none. Sharing a path would let one contract's credential deliver another.
func TestEveryObserverKindHasEndpoint(t *testing.T) {
	owners := map[string]collect.Kind{}
	for _, k := range collect.Kinds() {
		path := endpoint(k)
		if (path != "") != collect.IsObserver(k) {
			t.Errorf("%s: endpoint %q, registered as observer %t", k, path, collect.IsObserver(k))
		}
		if path == "" {
			continue
		}
		if other, taken := owners[path]; taken || !strings.HasPrefix(path, "/v1/") {
			t.Errorf("%s: endpoint %q is malformed or shared with %s", k, path, other)
		}
		owners[path] = k
	}
	if endpoint(Hook) != "/v1/hooks" || endpoint(MCP) != "/v1/mcp" {
		t.Error("installed senders post to the published endpoints")
	}
}
