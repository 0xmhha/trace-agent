package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Transcript layout under the root:
//
//	<project>/<session>.jsonl                          root transcript
//	<project>/<session>/subagents/agent-<agent>.jsonl  sub-agent transcript
const (
	transcriptExt   = ".jsonl"
	subagentsDir    = "subagents"
	agentFilePrefix = "agent-"
	rootDir         = "."
)

// transcriptOpenFlags never follows a symbolic link in the last path element
// and never blocks on a FIFO that replaced a file after it was listed.
const transcriptOpenFlags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

var (
	// errWithdrawn stops a pass whose consent was withdrawn while it ran.
	errWithdrawn = errors.New("usage/service: consent withdrawn")
	// errNotRegular reports a transcript path that is not a regular file.
	errNotRegular = errors.New("usage/service: not a regular file")
	// errScanOffsets reports a batch whose offsets do not describe the range
	// the scanner was asked to read.
	errScanOffsets = errors.New("usage/service: scanner returned invalid offsets")
)

// fileStamp is what this process last saw of a file it has dealt with. A file
// whose size and modification time still match is not opened again.
type fileStamp struct {
	size    int64
	modTime time.Time
}

// candidate is one transcript file found under the root.
type candidate struct {
	ref       usage.FileRef
	sessionID string
	agentID   string
}

// passState is the view of one scan pass.
type passState struct {
	epoch      uint64
	stopEpoch  uint64
	excluded   map[string]struct{}
	windowFrom time.Time
	// complete turns false when some file could not be dealt with for a
	// reason that is not recorded in the store.
	complete bool
	errs     []error
}

// pass performs one scan pass. The caller holds the scan gate.
func (s *Service) pass(ctx context.Context) (consented bool, err error) {
	// The epochs are read before the settings so that a withdrawal is seen
	// either in the settings or as a changed epoch, never missed.
	p := s.beginPass()
	settings, err := s.cfg.Store.Settings(ctx)
	if err != nil {
		return false, fmt.Errorf("read usage settings: %w", err)
	}
	if !settings.Consent {
		return false, nil
	}
	p.excluded = keySet(settings.Excluded)
	p.windowFrom = windowStart(s.cfg.Now(), settings.WindowDays)

	root, err := os.OpenRoot(s.cfg.Root)
	if errors.Is(err, fs.ErrNotExist) {
		// No transcripts yet: the pass is complete with nothing to read.
		s.setTotal(0)
		s.finishPass(p, nil)
		return true, nil
	}
	if err != nil {
		return true, fmt.Errorf("open transcript root: %w", err)
	}
	defer func() { _ = root.Close() }()

	files, err := discover(ctx, root, p)
	if err != nil {
		return true, err
	}
	s.setTotal(len(files))
	for _, file := range files {
		if err = ctx.Err(); err != nil {
			return true, err
		}
		if _, skip := p.excluded[file.ref.ProjectKey]; !skip {
			switch next, stopErr := s.settle(ctx, p, file, s.scanFile(ctx, root, p, file)); next {
			case stopWithdrawn:
				return false, nil
			case stopFailed:
				return true, stopErr
			case proceed:
			}
		}
		s.fileDone()
	}
	s.finishPass(p, files)
	return true, errors.Join(p.errs...)
}

// verdict is what a pass does after one file.
type verdict int

const (
	// proceed goes on with the next file.
	proceed verdict = iota
	// stopWithdrawn ends the pass because consent was withdrawn.
	stopWithdrawn
	// stopFailed ends the pass with an error.
	stopFailed
)

// settle acts on the outcome of one file and decides how the pass goes on.
// The error is set only with stopFailed.
func (s *Service) settle(ctx context.Context, p *passState, file candidate, fileErr error) (verdict, error) {
	switch {
	case fileErr == nil:
		return proceed, nil
	case ctx.Err() != nil:
		return stopFailed, ctx.Err()
	case errors.Is(fileErr, errWithdrawn):
		return stopWithdrawn, nil
	case errors.Is(fileErr, usage.ErrStale):
		return s.settleStale(ctx, p, file)
	}
	if markErr := s.cfg.Store.MarkUnreadable(ctx, file.ref); markErr != nil {
		if ctx.Err() != nil {
			return stopFailed, ctx.Err()
		}
		// The loss is recorded nowhere, so the pass must not count as done.
		p.complete = false
		p.errs = append(p.errs, fmt.Errorf("mark transcript unreadable: %w", errors.Join(markErr, fileErr)))
	}
	return proceed, nil
}

// settleStale handles a batch the store refused: consent, the excluded list
// or the cursor changed under the pass. It is not a failure of the file,
// which waits for the next pass.
func (s *Service) settleStale(ctx context.Context, p *passState, file candidate) (verdict, error) {
	settings, err := s.cfg.Store.Settings(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return stopFailed, ctx.Err()
		}
		return stopFailed, fmt.Errorf("read usage settings: %w", err)
	}
	if !settings.Consent {
		return stopWithdrawn, nil
	}
	p.excluded = keySet(settings.Excluded)
	if _, excluded := p.excluded[file.ref.ProjectKey]; !excluded {
		// The file was not brought up to date for a reason other than the
		// user excluding it, so this pass did not cover everything.
		p.complete = false
	}
	return proceed, nil
}

// scanFile brings one transcript up to date: it reads from the stored cursor
// in chunks and applies every batch. usage.ErrStale from Apply is returned
// wrapped; the caller decides what a failure means.
func (s *Service) scanFile(ctx context.Context, root *os.Root, p *passState, file candidate) error {
	if s.unchanged(file.ref) {
		return nil
	}
	cursor, tracked, err := s.cfg.Store.Cursor(ctx, file.ref.Path)
	if err != nil {
		return fmt.Errorf("read file cursor: %w", err)
	}
	if !tracked && !file.ref.ModTime.After(p.windowFrom) {
		// Outside the collection window. Remembering it saves a cursor
		// lookup per old file on every pass.
		s.remember(p, file.ref)
		return nil
	}
	f, info, err := openRegular(root, file.ref.Path)
	if err != nil {
		return fmt.Errorf("open transcript: %w", err)
	}
	defer func() { _ = f.Close() }()

	// The stamp is taken before reading: bytes appended while the file is
	// read then show as a change on the next pass.
	ref := file.ref
	ref.Size, ref.ModTime = info.Size(), info.ModTime()
	offset := cursor.Offset
	if offset < 0 || offset > ref.Size {
		// The file shrank, so it was rewritten: start over. Record keys
		// keep the second read from duplicating rows.
		offset = 0
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		if s.withdrawn(p) {
			return errWithdrawn
		}
		// The scanner may read ahead, so every chunk starts with a seek.
		if _, err = f.Seek(offset, io.SeekStart); err != nil {
			return fmt.Errorf("seek transcript: %w", err)
		}
		batch, scanErr := s.cfg.Scan(f, usage.ScanInput{
			SessionID: file.sessionID,
			AgentID:   file.agentID,
			Base:      offset,
			MaxBytes:  s.chunkBytes,
		})
		if scanErr != nil {
			return fmt.Errorf("scan transcript: %w", scanErr)
		}
		if batch.BaseOffset != offset || batch.NextOffset < offset {
			return errScanOffsets
		}
		if err = s.cfg.Store.Apply(ctx, ref, batch); err != nil {
			return fmt.Errorf("store batch: %w", err)
		}
		// Done when everything that was there at open has been read, or
		// when no complete line is left. Neither test relies on how long a
		// chunk the scanner chose to return.
		if batch.NextOffset >= ref.Size || batch.NextOffset == offset {
			break
		}
		offset = batch.NextOffset
	}
	s.remember(p, ref)
	return nil
}

// discover lists the transcript files of every project that is not excluded,
// projects and files in name order. A directory that cannot be listed is
// reported in p.errs and leaves the pass incomplete; a directory that
// vanished is not an error.
func discover(ctx context.Context, root *os.Root, p *passState) ([]candidate, error) {
	projects, err := readDir(root, rootDir)
	if err != nil {
		return nil, fmt.Errorf("list transcript root: %w", err)
	}
	var files []candidate
	for _, project := range projects {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if !project.IsDir() {
			continue
		}
		key := project.Name()
		if _, skip := p.excluded[key]; skip {
			continue
		}
		found, listErr := projectFiles(root, key)
		files = append(files, found...)
		if listErr != nil {
			p.complete = false
			p.errs = append(p.errs, fmt.Errorf("list project transcripts: %w", listErr))
		}
	}
	return files, nil
}

// projectFiles lists the root transcripts of one project directory and then
// the sub-agent transcripts of its session directories, so that a session is
// stored before the streams that hang off it.
func projectFiles(root *os.Root, project string) ([]candidate, error) {
	entries, err := readDir(root, project)
	if err != nil {
		return nil, ignoreMissing(err)
	}
	var (
		files, nested []candidate
		errs          []error
	)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			found, listErr := subagentFiles(root, project, name)
			nested = append(nested, found...)
			errs = append(errs, listErr)
			continue
		}
		sessionID, ok := strings.CutSuffix(name, transcriptExt)
		if !ok || sessionID == "" {
			continue
		}
		file, ok, statErr := newCandidate(entry, project, path.Join(project, name))
		errs = append(errs, statErr)
		if ok {
			file.sessionID = sessionID
			files = append(files, file)
		}
	}
	return append(files, nested...), errors.Join(errs...)
}

// subagentFiles lists <project>/<session>/subagents/*.jsonl. The session ID
// is the name of the directory that holds subagents.
func subagentFiles(root *os.Root, project, sessionID string) ([]candidate, error) {
	dir := path.Join(project, sessionID, subagentsDir)
	// Lstat, so that a symbolic link named subagents is not followed.
	info, err := root.Lstat(filepath.FromSlash(dir))
	if err != nil {
		return nil, ignoreMissing(err)
	}
	if !info.IsDir() {
		return nil, nil
	}
	entries, err := readDir(root, dir)
	if err != nil {
		return nil, ignoreMissing(err)
	}
	var (
		files []candidate
		errs  []error
	)
	for _, entry := range entries {
		name := entry.Name()
		stem, ok := strings.CutSuffix(name, transcriptExt)
		if !ok {
			continue
		}
		// A file that does not follow agent-<ID>.jsonl keeps its whole stem
		// as the agent ID, so it can never be mistaken for the root stream.
		agentID := strings.TrimPrefix(stem, agentFilePrefix)
		if agentID == "" {
			continue
		}
		file, ok, statErr := newCandidate(entry, project, path.Join(dir, name))
		errs = append(errs, statErr)
		if ok {
			file.sessionID, file.agentID = sessionID, agentID
			files = append(files, file)
		}
	}
	return files, errors.Join(errs...)
}

// newCandidate describes a listed entry. ok is false for anything that is not
// a regular file: symbolic links, FIFOs, devices and directories are judged
// by the entry type and are never opened.
func newCandidate(entry fs.DirEntry, project, rel string) (file candidate, ok bool, err error) {
	if !entry.Type().IsRegular() {
		return candidate{}, false, nil
	}
	info, err := entry.Info()
	if err != nil {
		return candidate{}, false, ignoreMissing(err)
	}
	if !info.Mode().IsRegular() {
		return candidate{}, false, nil
	}
	return candidate{ref: usage.FileRef{
		Path:       rel,
		ProjectKey: project,
		Size:       info.Size(),
		ModTime:    info.ModTime(),
	}}, true, nil
}

// readDir lists one directory under root, sorted by name so that the scan
// order does not depend on the file system.
func readDir(root *os.Root, dir string) ([]fs.DirEntry, error) {
	d, err := root.Open(filepath.FromSlash(dir))
	if err != nil {
		return nil, err
	}
	entries, err := d.ReadDir(-1)
	_ = d.Close()
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

// openRegular opens a root-relative path for reading and refuses anything
// that is not a regular file once it is open.
func openRegular(root *os.Root, rel string) (*os.File, fs.FileInfo, error) {
	f, err := root.OpenFile(filepath.FromSlash(rel), transcriptOpenFlags, 0)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errNotRegular
	}
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// ignoreMissing drops the error of an entry that disappeared between being
// listed and being read; transcripts are deleted while the scan runs.
func ignoreMissing(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// windowStart is the start of the collection window, computed the way the
// store computes it.
func windowStart(now time.Time, days int) time.Time {
	if days < 1 || days > usage.MaxWindowDays {
		days = usage.DefaultWindowDays
	}
	return now.AddDate(0, 0, -days)
}

func keySet(keys []string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

func (s *Service) beginPass() *passState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &passState{epoch: s.epoch, stopEpoch: s.stopEpoch, complete: true}
}

// finishPass clears the pending flag when the pass dealt with every file and
// no full pass became necessary while it ran. It also forgets files that are
// no longer listed.
func (s *Service) finishPass(p *passState, files []candidate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epoch != p.epoch {
		return
	}
	listed := make(map[string]struct{}, len(files))
	for _, file := range files {
		listed[file.ref.Path] = struct{}{}
	}
	for name := range s.seen {
		if _, ok := listed[name]; !ok {
			delete(s.seen, name)
		}
	}
	if p.complete {
		s.pending = false
	}
}

func (s *Service) withdrawn(p *passState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopEpoch != p.stopEpoch
}

func (s *Service) unchanged(ref usage.FileRef) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	stamp, ok := s.seen[ref.Path]
	return ok && stamp.size == ref.Size && stamp.modTime.Equal(ref.ModTime)
}

// remember notes that the file needs no work until it changes. A pass
// overtaken by an invalidation must not write: its view of the file is older
// than the reset it would hide.
func (s *Service) remember(p *passState, ref usage.FileRef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.epoch == p.epoch {
		s.seen[ref.Path] = fileStamp{size: ref.Size, modTime: ref.ModTime}
	}
}

func (s *Service) setTotal(total int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filesTotal, s.filesDone = total, 0
}

func (s *Service) fileDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filesDone++
}
