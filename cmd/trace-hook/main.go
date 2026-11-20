// Command trace-hook forwards the minimum Claude Code hook metadata to a local
// trace endpoint. It produces no decisions, messages or nonzero exit codes.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

func forward(ctx context.Context, input io.Reader, endpoint, token string) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "/v1/hooks" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || token == "" {
		return
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port == 0 {
		return
	}
	target := "http://127.0.0.1:" + strconv.FormatUint(port, 10) + "/v1/hooks"
	raw, err := io.ReadAll(io.LimitReader(input, 256<<10+1))
	if err != nil || len(raw) > 256<<10 {
		return
	}
	var msg struct {
		Event     string `json:"hook_event_name"`
		Session   string `json:"session_id"`
		Call      string `json:"tool_use_id"`
		Timestamp string `json:"timestamp,omitempty"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.Event == "" || msg.Session == "" || msg.Call == "" {
		return
	}
	projected, err := json.Marshal(msg)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(projected)) // #nosec G704 -- the host and path are constants and only a 16-bit port can vary.
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	client := http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// Only a validated numeric loopback authority and a fixed path can reach this call.
	resp, err := client.Do(req) // #nosec G704 -- address validation above prevents external destinations.
	if err == nil {
		_ = resp.Body.Close()
	}
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		forward(ctx, os.Stdin, os.Getenv("TRACE_OBSERVER_URL"), os.Getenv("TRACE_OBSERVER_TOKEN"))
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
