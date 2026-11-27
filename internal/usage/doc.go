// Package usage defines the contract of the usage-analytics pipeline.
//
// The pipeline is separate from the evidence store. It reads Claude Code
// transcripts, keeps numbers and metadata in its own SQLite file, and serves
// aggregated views to the desktop UI. Conversation text is never copied into
// that file; the session timeline is read from the original transcript on
// demand and masked before display.
//
// This package holds only shared types and small ports. Implementations live
// in the sub-packages:
//
//	transcript  parses transcript lines into a Batch
//	timeline    builds the display timeline of one transcript
//	db          persists batches and answers aggregate queries
//	insight     turns aggregates into insights and recommendations
//	service     schedules scans and dispatches UI requests
package usage
