package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// smallChunk makes the synthetic transcripts span several chunks.
const smallChunk = 512

func TestScanOnceReadsRootAndSubagentTranscripts(t *testing.T) {
	h := newHarness(t)
	rootSize, agentSize := h.fixtures()
	if !h.status().Scanning {
		t.Fatal("a new service must report a pending full pass")
	}
	h.scanOnce()

	wantInputs := []usage.ScanInput{
		{SessionID: testSession, Base: 0, MaxBytes: usage.ScanChunkBytes},
		{SessionID: testSession, AgentID: testAgent, Base: 0, MaxBytes: usage.ScanChunkBytes},
	}
	if got := h.scans.calls(); !slices.Equal(got, wantInputs) {
		t.Fatalf("scan inputs = %+v\nwant %+v", got, wantInputs)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.applied) != 2 {
			t.Fatalf("applied %d batches; want 2", len(f.applied))
		}
		for i, want := range []struct {
			rel  string
			size int64
		}{{rootRel, rootSize}, {agentRel, agentSize}} {
			file, batch := f.applied[i].file, f.applied[i].batch
			if file.Path != want.rel || file.ProjectKey != testProject || file.Size != want.size {
				t.Errorf("batch %d file = %+v; want %s of %d bytes in %s", i, file, want.rel, want.size, testProject)
			}
			if !file.ModTime.Equal(testNow.Add(-time.Hour)) {
				t.Errorf("batch %d mod time = %v", i, file.ModTime)
			}
			if batch.BaseOffset != 0 || batch.NextOffset != want.size {
				t.Errorf("batch %d offsets = %d..%d; want 0..%d", i, batch.BaseOffset, batch.NextOffset, want.size)
			}
			if cursor := f.cursors[want.rel]; cursor.Offset != want.size {
				t.Errorf("cursor of %s = %d; want %d", want.rel, cursor.Offset, want.size)
			}
		}
		if len(f.marked) != 0 {
			t.Errorf("marked unreadable: %+v", f.marked)
		}
	})
	status := h.status()
	if status.Scanning || status.FilesTotal != 2 || status.FilesDone != 2 {
		t.Fatalf("status after the pass = scanning %v, %d/%d; want idle 2/2", status.Scanning, status.FilesDone, status.FilesTotal)
	}
}

func TestScanOnceReadsInChunksAndResumes(t *testing.T) {
	h := newHarness(t)
	h.svc.chunkBytes = smallChunk
	content := h.write(rootRel, 40)
	size := int64(len(content))
	h.scanOnce()

	calls := h.scans.calls()
	if len(calls) < 3 {
		t.Fatalf("a %d-byte file was read in %d calls; want several chunks of %d", size, len(calls), smallChunk)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.applied) != len(calls) {
			t.Fatalf("applied %d batches for %d scans; every chunk must be applied", len(f.applied), len(calls))
		}
		next := int64(0)
		for i, in := range calls {
			if in.Base != next || in.MaxBytes != smallChunk {
				t.Fatalf("scan %d = base %d, max %d; want base %d, max %d", i, in.Base, in.MaxBytes, next, smallChunk)
			}
			batch := f.applied[i].batch
			if span := batch.NextOffset - batch.BaseOffset; i < len(calls)-1 && span < smallChunk {
				t.Fatalf("chunk %d holds %d bytes; only the last one may be short", i, span)
			}
			next = batch.NextOffset
		}
		if next != size {
			t.Fatalf("chunks end at %d; want the file size %d", next, size)
		}
	})

	// Appending moves the file on; the next pass starts at the cursor.
	h.scans.reset()
	grown := append(slices.Clone(content), content[:len(content)/40]...)
	h.put(rootRel, grown)
	h.scanOnce()
	calls = h.scans.calls()
	if len(calls) != 1 || calls[0].Base != size {
		t.Fatalf("after an append the scan = %+v; want one call from %d", calls, size)
	}

	// A file shorter than its cursor was rewritten and is read from the start.
	h.scans.reset()
	h.put(rootRel, content[:len(content)/40])
	h.scanOnce()
	calls = h.scans.calls()
	if len(calls) != 1 || calls[0].Base != 0 {
		t.Fatalf("after a shrink the scan = %+v; want one call from 0", calls)
	}
	h.store.with(func(f *fakeStore) {
		if last := f.applied[len(f.applied)-1].batch; last.BaseOffset != 0 {
			t.Fatalf("restart batch base = %d; want 0", last.BaseOffset)
		}
	})
}

func TestScanOnceEndsAFileWithoutAnExtraRead(t *testing.T) {
	h := newHarness(t)
	line := int64(len(h.write(rootRel, 1)))
	h.svc.chunkBytes = line
	content := h.write(rootRel, 3)
	h.scanOnce()
	// The file ends exactly where the third chunk ends.
	want := []string{testSession + "/@0", testSession + "/@" + itoa(line), testSession + "/@" + itoa(2*line)}
	if got := h.scanned(); !slices.Equal(got, want) {
		t.Fatalf("scans = %v\nwant %v", got, want)
	}

	// A last line that is still being written is left for a later pass.
	h.scans.reset()
	h.put(rootRel, append(slices.Clone(content), `{"type":"sample","note":"unfinis`...))
	h.scanOnce()
	if got, want := h.scanned(), []string{testSession + "/@" + itoa(3*line)}; !slices.Equal(got, want) {
		t.Fatalf("scans of a file with a partial line = %v\nwant %v", got, want)
	}
	h.store.with(func(f *fakeStore) {
		if cursor := f.cursors[rootRel]; cursor.Offset != 3*line {
			t.Fatalf("cursor = %d; the partial line must stay unread at %d", cursor.Offset, 3*line)
		}
	})
}

func TestScanOnceSkipsUnchangedFiles(t *testing.T) {
	h := newHarness(t)
	h.fixtures()
	h.scanOnce()
	h.scans.reset()
	h.store.with(func(f *fakeStore) { f.cursorLookups = nil })

	h.scanOnce()
	if calls := h.scans.calls(); len(calls) != 0 {
		t.Fatalf("an unchanged tree was scanned again: %+v", calls)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.cursorLookups) != 0 {
			t.Fatalf("unchanged files cost cursor lookups: %v", f.cursorLookups)
		}
	})
	if status := h.status(); status.FilesTotal != 2 || status.FilesDone != 2 {
		t.Fatalf("progress = %d/%d; want 2/2", status.FilesDone, status.FilesTotal)
	}

	// Only the modification time moved: the file is opened again.
	h.touch(rootRel, testNow.Add(-time.Minute))
	h.scanOnce()
	if got := len(h.scans.calls()); got != 1 {
		t.Fatalf("a touched file caused %d scans; want 1", got)
	}
}

func TestScanOnceWithoutConsentReadsNothing(t *testing.T) {
	h := newHarness(t)
	h.fixtures()
	h.store.with(func(f *fakeStore) { f.settings.Consent = false })
	h.scanOnce()
	if calls := h.scans.calls(); len(calls) != 0 {
		t.Fatalf("scanned without consent: %+v", calls)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.cursorLookups)+len(f.applied)+len(f.marked) != 0 {
			t.Fatal("the store was used without consent")
		}
	})
	if status := h.status(); status.Scanning || status.FilesTotal != 0 {
		t.Fatalf("status without consent = %+v; want nothing in progress", status)
	}
}

func TestScanOnceSkipsExcludedProjects(t *testing.T) {
	h := newHarness(t)
	h.write(rootRel, 2)
	h.write(otherProject+"/s2.jsonl", 2)
	h.write(otherProject+"/s2/subagents/agent-x.jsonl", 2)
	h.store.with(func(f *fakeStore) { f.settings.Excluded = []string{otherProject} })
	h.scanOnce()
	if got, want := h.scanned(), []string{testSession + "/@0"}; !slices.Equal(got, want) {
		t.Fatalf("scans = %v; want %v", got, want)
	}
	if status := h.status(); status.FilesTotal != 1 {
		t.Fatalf("files total = %d; excluded files must not be counted", status.FilesTotal)
	}
}

func TestScanOnceIgnoresEverythingButRegularTranscripts(t *testing.T) {
	h := newHarness(t)
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.write(rootRel, 1)
	h.write(testProject+"/notes.txt", 1)
	h.write(testProject+"/.jsonl", 1)
	h.write("stray.jsonl", 1)
	h.write(testProject+"/s3/subagents/agent-.jsonl", 1)
	h.write(testProject+"/s3/subagents/agent-ok.jsonl", 1)
	h.write(testProject+"/s3/subagents/freeform.jsonl", 1)
	h.write(testProject+"/s3/subagents/deeper/agent-deep.jsonl", 1)
	h.write(testProject+"/s3/other/agent-elsewhere.jsonl", 1)
	h.write(testProject+"/s4target/subagents/agent-linked.jsonl", 1)
	project := filepath.Join(h.root, testProject)
	for link, target := range map[string]string{
		filepath.Join(project, "link-out.jsonl"):       outside,
		filepath.Join(project, "link-in.jsonl"):        filepath.Join(h.root, filepath.FromSlash(rootRel)),
		filepath.Join(h.root, "linked-project"):        project,
		filepath.Join(project, "s5"):                   filepath.Join(project, "s4target"),
		filepath.Join(project, "s3", "subagents-link"): filepath.Join(project, "s3", "subagents"),
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	// A directory whose only subagents entry is a symbolic link.
	if err := os.Mkdir(filepath.Join(project, "s6"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(project, "s3", "subagents"), filepath.Join(project, "s6", "subagents")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(project, "dir.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Opening a FIFO for reading would block for ever; it must be skipped by
	// its entry type alone.
	if err := syscall.Mkfifo(filepath.Join(project, "pipe.jsonl"), 0o600); err != nil {
		t.Fatalf("create fifo: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- h.svc.ScanOnce(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ScanOnce: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the pass hangs, most likely on the FIFO")
	}
	want := []string{
		testSession + "/@0",
		"s3/ok@0",
		"s3/freeform@0",
		"s4target/linked@0",
	}
	if got := h.scanned(); !slices.Equal(got, want) {
		t.Fatalf("scans = %v\nwant %v", got, want)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.marked) != 0 {
			t.Fatalf("ignored entries were marked unreadable: %+v", f.marked)
		}
	})
}

func TestScanOnceMarksFailedFilesAndContinues(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not stop the superuser")
	}
	h := newHarness(t)
	const (
		readFails   = testProject + "/a-read.jsonl"
		badOffsets  = testProject + "/b-offsets.jsonl"
		cursorFails = testProject + "/c-cursor.jsonl"
		applyFails  = testProject + "/d-apply.jsonl"
		noAccess    = testProject + "/e-denied.jsonl"
		healthy     = testProject + "/f-fine.jsonl"
	)
	for _, rel := range []string{readFails, badOffsets, cursorFails, applyFails, noAccess, healthy} {
		h.write(rel, 2)
	}
	if err := os.Chmod(filepath.Join(h.root, filepath.FromSlash(noAccess)), 0); err != nil {
		t.Fatal(err)
	}
	h.scans.setHook(func(_ int, in usage.ScanInput) error {
		if in.SessionID == "a-read" {
			return errInjected
		}
		return nil
	})
	h.scans.result = func(batch usage.Batch) usage.Batch {
		if batch.Session.SessionID == "b-offsets" {
			batch.NextOffset = batch.BaseOffset - 1
		}
		return batch
	}
	h.store.with(func(f *fakeStore) {
		f.fail["Cursor:"+cursorFails] = errInjected
		f.beforeApply = func(_ *fakeStore, file usage.FileRef, _ usage.Batch) error {
			if file.Path == applyFails {
				return errInjected
			}
			return nil
		}
	})

	h.scanOnce()
	failed := []string{readFails, badOffsets, cursorFails, applyFails, noAccess}
	h.store.with(func(f *fakeStore) {
		var marked []string
		for _, file := range f.marked {
			if file.ProjectKey != testProject {
				t.Errorf("marked file %s has project %q", file.Path, file.ProjectKey)
			}
			marked = append(marked, file.Path)
		}
		if !slices.Equal(marked, failed) {
			t.Fatalf("marked unreadable = %v\nwant %v", marked, failed)
		}
		if len(f.applied) != 1 || f.applied[0].file.Path != healthy {
			t.Fatalf("applied = %+v; want only %s", f.applied, healthy)
		}
	})
	// The losses are on record in the store, so the full pass is done.
	if status := h.status(); status.Scanning || status.FilesDone != 6 {
		t.Fatalf("status = scanning %v, done %d; want an idle pass over 6 files", status.Scanning, status.FilesDone)
	}

	// A failed file is tried again on the next pass; a good one is not.
	h.scans.reset()
	h.scans.setHook(nil)
	h.scanOnce()
	if got, want := h.scanned(), []string{"a-read/@0", "b-offsets/@0", "d-apply/@0"}; !slices.Equal(got, want) {
		t.Fatalf("second pass scans = %v\nwant %v", got, want)
	}
}

func TestScanOnceReportsALossItCannotRecord(t *testing.T) {
	h := newHarness(t)
	h.write(rootRel, 1)
	h.write(otherProject+"/s2.jsonl", 1)
	h.scans.setHook(func(_ int, in usage.ScanInput) error {
		if in.SessionID == testSession {
			return errInjected
		}
		return nil
	})
	h.store.with(func(f *fakeStore) { f.fail["MarkUnreadable"] = errInjected })
	err := h.svc.ScanOnce(context.Background())
	if !errors.Is(err, errInjected) {
		t.Fatalf("ScanOnce = %v; want the store failure", err)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.applied) != 1 || f.applied[0].file.Path != otherProject+"/s2.jsonl" {
			t.Fatalf("applied = %+v; the pass must go on after the failure", f.applied)
		}
	})
	if !h.status().Scanning {
		t.Fatal("a pass with an unrecorded loss must not count as a full pass")
	}
}

func TestScanOnceHandlesStaleBatches(t *testing.T) {
	const (
		first  = testProject + "/a.jsonl"
		second = testProject + "/b.jsonl"
		third  = otherProject + "/c.jsonl"
	)
	for _, tc := range []struct {
		name string
		// onFirst runs inside Apply of the first file.
		onFirst      func(f *fakeStore) error
		wantScans    []string
		wantApplied  []string
		wantScanning bool
	}{
		{
			name:         "cursor moved under the pass",
			onFirst:      func(*fakeStore) error { return usage.ErrStale },
			wantScans:    []string{"a/@0", "b/@0", "c/@0"},
			wantApplied:  []string{second, third},
			wantScanning: true,
		},
		{
			name: "project excluded during the pass",
			onFirst: func(f *fakeStore) error {
				f.settings.Excluded = []string{testProject}
				return nil
			},
			wantScans:   []string{"a/@0", "c/@0"},
			wantApplied: []string{third},
		},
		{
			name: "consent withdrawn in the store",
			onFirst: func(f *fakeStore) error {
				f.settings.Consent = false
				return nil
			},
			wantScans:    []string{"a/@0"},
			wantScanning: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			for _, rel := range []string{first, second, third} {
				h.write(rel, 2)
			}
			h.store.with(func(f *fakeStore) {
				f.beforeApply = func(f *fakeStore, file usage.FileRef, _ usage.Batch) error {
					if file.Path == first {
						return tc.onFirst(f)
					}
					return nil
				}
			})
			h.scanOnce()
			if got := h.scanned(); !slices.Equal(got, tc.wantScans) {
				t.Fatalf("scans = %v\nwant %v", got, tc.wantScans)
			}
			h.store.with(func(f *fakeStore) {
				var applied []string
				for _, a := range f.applied {
					applied = append(applied, a.file.Path)
				}
				if !slices.Equal(applied, tc.wantApplied) {
					t.Fatalf("applied = %v\nwant %v", applied, tc.wantApplied)
				}
				if len(f.marked) != 0 {
					t.Fatalf("a stale batch marked the file unreadable: %+v", f.marked)
				}
			})
			h.store.with(func(f *fakeStore) { f.settings.Consent = true })
			if got := h.status().Scanning; got != tc.wantScanning {
				t.Fatalf("scanning after the pass = %v; want %v", got, tc.wantScanning)
			}
		})
	}
}

func TestScanOnceStopsWhenSettingsCannotBeReadAfterAStaleBatch(t *testing.T) {
	h := newHarness(t)
	h.write(rootRel, 2)
	h.write(otherProject+"/later.jsonl", 2)
	h.store.with(func(f *fakeStore) {
		f.beforeApply = func(f *fakeStore, _ usage.FileRef, _ usage.Batch) error {
			f.fail["Settings"] = errInjected
			return usage.ErrStale
		}
	})
	if err := h.svc.ScanOnce(context.Background()); !errors.Is(err, errInjected) {
		t.Fatalf("ScanOnce = %v; want the settings failure", err)
	}
	if got := len(h.scans.calls()); got != 1 {
		t.Fatalf("%d files were read; the pass cannot go on without knowing whether collection is allowed", got)
	}
}

func TestWindowStart(t *testing.T) {
	for _, tc := range []struct {
		name string
		days int
		want time.Time
	}{
		{"configured days", 7, testNow.AddDate(0, 0, -7)},
		{"widest window", usage.MaxWindowDays, testNow.AddDate(0, 0, -usage.MaxWindowDays)},
		{"zero falls back to the default", 0, testNow.AddDate(0, 0, -usage.DefaultWindowDays)},
		{"too wide falls back to the default", usage.MaxWindowDays + 1, testNow.AddDate(0, 0, -usage.DefaultWindowDays)},
	} {
		if got := windowStart(testNow, tc.days); !got.Equal(tc.want) {
			t.Errorf("%s: windowStart = %v; want %v", tc.name, got, tc.want)
		}
	}
}

func TestScanOnceAppliesTheWindowRule(t *testing.T) {
	h := newHarness(t)
	const (
		recent   = testProject + "/recent.jsonl"
		old      = testProject + "/old.jsonl"
		tracked  = testProject + "/tracked.jsonl"
		boundary = testProject + "/boundary.jsonl"
	)
	windowFrom := testNow.AddDate(0, 0, -usage.DefaultWindowDays)
	for _, rel := range []string{recent, old, tracked, boundary} {
		h.write(rel, 2)
	}
	h.touch(old, windowFrom.Add(-time.Hour))
	h.touch(tracked, windowFrom.Add(-time.Hour))
	h.touch(boundary, windowFrom)
	h.store.with(func(f *fakeStore) { f.cursors[tracked] = usage.FileCursor{} })

	h.scanOnce()
	if got, want := h.scanned(), []string{"recent/@0", "tracked/@0"}; !slices.Equal(got, want) {
		t.Fatalf("scans = %v\nwant %v", got, want)
	}
	if status := h.status(); status.Scanning || status.FilesDone != 4 {
		t.Fatalf("status = %+v; files outside the window still count as dealt with", status)
	}

	// Files outside the window are not looked up again while nothing changes.
	h.store.with(func(f *fakeStore) { f.cursorLookups = nil })
	h.scanOnce()
	h.store.with(func(f *fakeStore) {
		if len(f.cursorLookups) != 0 {
			t.Fatalf("cursor lookups on the second pass: %v", f.cursorLookups)
		}
	})

	// A wider window brings them in and makes a full pass pending.
	h.scans.reset()
	status := call[usage.Status](t, h.svc, usage.MethodSetWindow, `{"days":60}`)
	if !status.Scanning || status.WindowDays != 60 {
		t.Fatalf("status after widening = %+v; want scanning with 60 days", status)
	}
	h.scanOnce()
	if got, want := h.scanned(), []string{"boundary/@0", "old/@0", "recent/@" + itoa(sizeOf(t, h, recent)), "tracked/@" + itoa(sizeOf(t, h, tracked))}; !slices.Equal(got, want) {
		t.Fatalf("scans after widening = %v\nwant %v", got, want)
	}
	if h.status().Scanning {
		t.Fatal("the pass after widening must clear the pending flag")
	}

	// A narrower window asks for nothing.
	if status = call[usage.Status](t, h.svc, usage.MethodSetWindow, `{"days":7}`); status.Scanning {
		t.Fatal("narrowing the window must not start a full pass")
	}
}

func TestScanOnceStopsOnCancelMidFile(t *testing.T) {
	h := newHarness(t)
	h.svc.chunkBytes = smallChunk
	h.write(rootRel, 40)
	h.write(otherProject+"/later.jsonl", 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.scans.setHook(func(n int, _ usage.ScanInput) error {
		if n == 1 {
			cancel()
		}
		return nil
	})
	if err := h.svc.ScanOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanOnce = %v; want context.Canceled", err)
	}
	if got := len(h.scans.calls()); got != 2 {
		t.Fatalf("%d scans ran; the pass must stop at the chunk boundary after the cancel", got)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.marked) != 0 {
			t.Fatalf("a cancelled read marked the file unreadable: %+v", f.marked)
		}
	})
	if !h.status().Scanning {
		t.Fatal("a cancelled pass must leave the full pass pending")
	}

	// The next pass picks the file up at the stored cursor.
	h.scans.reset()
	h.scans.setHook(nil)
	h.scanOnce()
	calls := h.scans.calls()
	var resumeAt int64
	h.store.with(func(f *fakeStore) { resumeAt = f.applied[1].batch.NextOffset })
	if len(calls) == 0 || calls[0].Base != resumeAt {
		t.Fatalf("resume scans = %+v; want the first from %d", calls, resumeAt)
	}
	if h.status().Scanning {
		t.Fatal("the completed pass must clear the pending flag")
	}
}

func TestSetConsentWithdrawalStopsARunningPass(t *testing.T) {
	h := newHarness(t)
	h.svc.chunkBytes = smallChunk
	h.write(rootRel, 40)
	h.write(otherProject+"/later.jsonl", 2)
	h.scans.setHook(func(n int, _ usage.ScanInput) error {
		if n == 0 {
			return h.svc.SetConsent(context.Background(), false)
		}
		return nil
	})
	h.scanOnce()
	if got := len(h.scans.calls()); got != 1 {
		t.Fatalf("%d scans ran after consent was withdrawn; want the pass to stop at once", got)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.applied)+len(f.marked) != 0 {
			t.Fatalf("stored after withdrawal: applied %d, marked %d", len(f.applied), len(f.marked))
		}
	})

	// Granting again starts over with a full pass.
	h.scans.setHook(nil)
	if err := h.svc.SetConsent(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if !h.status().Scanning {
		t.Fatal("granting consent must make a full pass pending")
	}
	h.scanOnce()
	if status := h.status(); status.Scanning || !status.Consent {
		t.Fatalf("status after the pass = %+v", status)
	}
}

// The store refuses batches once consent is gone, but reading must stop too:
// the service checks at every chunk boundary, before the next read.
func TestWithdrawalIsSeenAtTheChunkBoundary(t *testing.T) {
	h := newHarness(t)
	h.svc.chunkBytes = smallChunk
	h.write(rootRel, 40)
	h.write(otherProject+"/later.jsonl", 2)
	h.scans.setHook(func(n int, _ usage.ScanInput) error {
		if n == 0 {
			h.svc.withdraw()
		}
		return nil
	})
	h.scanOnce()
	if got := len(h.scans.calls()); got != 1 {
		t.Fatalf("%d chunks were read; want reading to stop after the chunk in progress", got)
	}
	h.store.with(func(f *fakeStore) {
		if len(f.marked) != 0 {
			t.Fatalf("a withdrawal marked the file unreadable: %+v", f.marked)
		}
	})
}

func TestSetConsentReturnsStoreFailure(t *testing.T) {
	h := newHarness(t)
	h.store.with(func(f *fakeStore) { f.fail["SetConsent"] = errInjected })
	if err := h.svc.SetConsent(context.Background(), true); !errors.Is(err, errInjected) {
		t.Fatalf("SetConsent = %v; want the store failure", err)
	}
}

func TestScanOnceWithoutRootSucceeds(t *testing.T) {
	h := newHarness(t, func(cfg *Config) { cfg.Root = filepath.Join(cfg.Root, "missing") })
	h.scanOnce()
	if status := h.status(); status.Scanning || status.FilesTotal != 0 {
		t.Fatalf("status = %+v; a missing root is a complete pass over nothing", status)
	}
}

func TestScanOnceFailsWhenTheRootIsNotADirectory(t *testing.T) {
	h := newHarness(t)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.svc.cfg.Root = file
	if err := h.svc.ScanOnce(context.Background()); err == nil {
		t.Fatal("ScanOnce accepted a root that is a file")
	}
	if !h.status().Scanning {
		t.Fatal("a failed pass must leave the full pass pending")
	}
}

func TestScanOnceReportsAProjectItCannotList(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not stop the superuser")
	}
	h := newHarness(t)
	h.write(rootRel, 1)
	h.write(otherProject+"/s2.jsonl", 1)
	locked := filepath.Join(h.root, testProject)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	if err := h.svc.ScanOnce(context.Background()); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("ScanOnce = %v; want a permission error", err)
	}
	if got, want := h.scanned(), []string{"s2/@0"}; !slices.Equal(got, want) {
		t.Fatalf("scans = %v; the other project must still be read", got)
	}
	if !h.status().Scanning {
		t.Fatal("a pass that could not list a project is not a full pass")
	}
}

func TestScanOnceReturnsStoreFailures(t *testing.T) {
	h := newHarness(t)
	h.fixtures()
	h.store.with(func(f *fakeStore) { f.fail["Settings"] = errInjected })
	if err := h.svc.ScanOnce(context.Background()); !errors.Is(err, errInjected) {
		t.Fatalf("ScanOnce = %v; want the settings failure", err)
	}
	if calls := h.scans.calls(); len(calls) != 0 {
		t.Fatalf("scanned without knowing the settings: %+v", calls)
	}
}

func TestScanPassesNeverOverlap(t *testing.T) {
	const (
		first  = testProject + "/a.jsonl"
		second = testProject + "/b.jsonl"
	)
	h := newHarness(t)
	h.write(first, 2)
	h.write(second, 2)
	h.scanOnce()
	h.scans.reset()
	sizeA, sizeB := sizeOf(t, h, first), sizeOf(t, h, second)
	// The file grew, so the next pass reads it from a cursor that is not 0.
	h.write(first, 3)

	entered, release := make(chan struct{}), make(chan struct{})
	var (
		active, peak atomic.Int32
		blockNext    atomic.Bool
	)
	blockNext.Store(true)
	h.scans.setHook(func(int, usage.ScanInput) error {
		raise(&peak, active.Add(1))
		defer active.Add(-1)
		if blockNext.CompareAndSwap(true, false) {
			entered <- struct{}{}
			<-release
		}
		return nil
	})
	running := make(chan error, 1)
	go func() { running <- h.svc.ScanOnce(context.Background()) }()
	<-entered

	// While the first pass holds the gate, a second caller can only give up.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.svc.ScanOnce(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("second ScanOnce = %v; want it to wait for the gate and see the cancel", err)
	}

	// A rescan requested now must survive the pass that is still running.
	if status := call[usage.Status](t, h.svc, usage.MethodRescan, ""); !status.Scanning {
		t.Fatal("rescan must make a full pass pending")
	}
	close(release)
	if err := <-running; err != nil {
		t.Fatalf("first ScanOnce: %v", err)
	}
	if !h.status().Scanning {
		t.Fatal("a pass that started before the rescan cleared its pending flag")
	}

	h.scanOnce()
	if h.status().Scanning {
		t.Fatal("the pass after the rescan must clear the pending flag")
	}
	// The overtaken pass had its batch for the first file refused and read
	// the second one from the start; the next pass does the first file over.
	want := []string{"a/@" + itoa(sizeA), "b/@0", "a/@0", "b/@" + itoa(sizeB)}
	if got := h.scanned(); !slices.Equal(got, want) {
		t.Fatalf("scans = %v\nwant %v", got, want)
	}
	if got := peak.Load(); got != 1 {
		t.Fatalf("%d scans ran at once; want 1", got)
	}
}

func TestReincludingAProjectReadsItAgain(t *testing.T) {
	h := newHarness(t)
	h.write(rootRel, 2)
	h.scanOnce()
	h.scans.reset()

	if status := call[usage.Status](t, h.svc, usage.MethodSetExcluded, `{"project":"`+testProject+`","excluded":true}`); status.Scanning {
		t.Fatal("excluding a project must not start a full pass")
	}
	h.scanOnce()
	if calls := h.scans.calls(); len(calls) != 0 {
		t.Fatalf("an excluded project was scanned: %+v", calls)
	}

	status := call[usage.Status](t, h.svc, usage.MethodSetExcluded, `{"project":"`+testProject+`","excluded":false}`)
	if !status.Scanning || len(status.Excluded) != 0 {
		t.Fatalf("status after including again = %+v; want a pending full pass", status)
	}
	h.scanOnce()
	if got, want := h.scanned(), []string{testSession + "/@0"}; !slices.Equal(got, want) {
		t.Fatalf("scans = %v; the unchanged file must be read again from the start", got)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func sizeOf(t *testing.T, h *harness, rel string) int64 {
	t.Helper()
	info, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}
