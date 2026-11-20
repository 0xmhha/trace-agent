package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestForwardMetadataOnly(t *testing.T) {
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		received <- string(b)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	raw := `{"hook_event_name":"PreToolUse","session_id":"s","tool_use_id":"t","tool_input":{"secret":"private"}}`
	forward(ctx, strings.NewReader(raw), server.URL+"/v1/hooks", "test")
	select {
	case got := <-received:
		if strings.Contains(got, "private") || strings.Contains(got, "tool_input") {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	server.Close()
	forward(ctx, strings.NewReader(raw), server.URL+"/v1/hooks", "test")
}
