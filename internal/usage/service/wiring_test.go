package service_test

import (
	"github.com/0xmhha/trace-agent/internal/usage/db"
	"github.com/0xmhha/trace-agent/internal/usage/insight"
	"github.com/0xmhha/trace-agent/internal/usage/service"
	"github.com/0xmhha/trace-agent/internal/usage/timeline"
	"github.com/0xmhha/trace-agent/internal/usage/transcript"
)

// Compile-time checks that the implementations match the service ports.
var (
	_ service.Store        = (*db.Store)(nil)
	_ service.ScanFunc     = transcript.Scan
	_ service.TimelineFunc = timeline.Read
	_ service.EvaluateFunc = insight.Evaluate
)
