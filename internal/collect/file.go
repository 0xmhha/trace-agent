package collect

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"syscall"
)

const anchorSize = 256

// FileCursor is the last accepted complete-line boundary. Persist it together
// with its evidence. The zero value starts a new stream.
type FileCursor struct {
	Source, Stream, FileID, Generation, Anchor string
	Offset                                     int64
}

// FileCommit must accept the evidence and proposed cursor together, or return an
// error. After an ambiguous failure retry with the last durable cursor.
type FileCommit func(context.Context, Evidence, FileCursor) error

// ReadFile reads a bounded snapshot beneath the user's selected root. A trailing
// fragment is left unread until the next invocation. Calls for a stream are serial.
func (a *Adapter) ReadFile(ctx context.Context, root *os.Root, name string, saved FileCursor, commit FileCommit) (FileCursor, error) {
	if err := ctx.Err(); err != nil {
		return saved, err
	}
	if root == nil || commit == nil || a.kind == AppServer {
		return saved, errInput
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return saved, errors.New("cannot open selected stream")
	}
	defer func() { _ = f.Close() }()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		return saved, errors.New("not a readable regular file")
	}
	sys, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return saved, errors.New("unsupported file identity")
	}
	stream := a.token("stream", a.source, name)
	fileID := a.token("file", fmt.Sprint(sys.Dev), strconv.FormatUint(sys.Ino, 10))
	cursor := saved
	if saved != (FileCursor{}) && (saved.Source != a.source || saved.Stream != stream || saved.Offset < 0) {
		return saved, errors.New("checkpoint scope mismatch")
	}
	rotate := cursor.FileID != fileID || cursor.Offset > stat.Size()
	var tail []byte
	if !rotate {
		n := min(cursor.Offset, int64(anchorSize))
		tail = make([]byte, n)
		if _, err = f.ReadAt(tail, cursor.Offset-n); err != nil {
			return saved, errors.New("cannot verify checkpoint")
		}
		rotate = cursor.Anchor != a.token("anchor", string(tail))
	}
	if rotate {
		cursor = FileCursor{Source: a.source, Stream: stream, FileID: fileID, Generation: a.token("file-generation", fileID, saved.Generation)}
		tail = nil
		cursor.Anchor = a.token("anchor", "")
		if saved != (FileCursor{}) {
			reason := "rewritten"
			switch {
			case saved.FileID != fileID:
				reason = "replaced"
			case saved.Offset > stat.Size():
				reason = "truncated"
			}
			// Even an empty replacement invalidates the old offset. Persist the
			// gap and the new zero boundary before attempting its first line.
			gap := a.evidence(Position{Stream: name, Generation: cursor.Generation}, object{"type": "source_gap", "reason": reason}, nil)
			gap.Observer = "collector"
			gap.PreviousGeneration = saved.Generation
			gap.Generation = cursor.Generation
			gap.RecordKey = a.token("file-gap", stream, cursor.Generation)
			if err := commit(ctx, gap, cursor); err != nil {
				return saved, err
			}
			saved = cursor
		}
	}
	// SectionReader fixes the observed size; data appended during this poll is read next time.
	reader := bufio.NewReaderSize(io.NewSectionReader(f, cursor.Offset, stat.Size()-cursor.Offset), 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return saved, err
		}
		line, err := completeLine(reader)
		if errors.Is(err, io.EOF) {
			return saved, nil
		}
		if err != nil {
			return saved, err
		}
		obs, err := a.Project(Position{Stream: name, Generation: cursor.Generation, Offset: cursor.Offset}, line[:len(line)-1])
		if err != nil {
			return saved, err
		}
		obs.Generation = cursor.Generation
		cursor.Offset += int64(len(line))
		tail = append(tail, line[max(0, len(line)-anchorSize):]...)
		tail = tail[max(0, len(tail)-anchorSize):]
		cursor.Anchor = a.token("anchor", string(tail))
		if err := commit(ctx, obs, cursor); err != nil {
			return saved, err
		}
		saved = cursor
	}
}

func completeLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := r.ReadSlice('\n')
		if len(line)+len(part) > maxRecord+1 {
			return nil, errLimit
		}
		line = append(line, part...)
		switch {
		case err == nil:
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return nil, io.EOF
		default:
			return nil, errors.New("cannot read selected stream")
		}
	}
}
