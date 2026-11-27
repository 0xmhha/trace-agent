package transcript

import (
	"bufio"
	"errors"
	"io"
)

// readBufferBytes is the size of the read buffer. A line is assembled from
// buffer-sized pieces, so this is also the step in which an oversized line is
// discarded.
const readBufferBytes = 64 << 10

// limits bounds how much of one line the scanner keeps. Tests inject small
// values; Scan uses the documented ones.
type limits struct {
	// line is the longest line, without its newline, that is still parsed.
	line int
	// sniff is how much of a longer line is kept to find out its type.
	sniff int
}

// line is one newline-terminated line of the transcript. data excludes the
// newline and is valid until the next read. For an oversized line it holds
// only the leading limits.sniff bytes. size is the number of bytes the line
// occupies in the file, newline included.
type line struct {
	data      []byte
	size      int64
	oversized bool
}

// lineReader reads newline-terminated lines without ever holding more than
// limits.line bytes of one of them.
type lineReader struct {
	source *bufio.Reader
	limits limits
	buf    []byte
}

func newLineReader(r io.Reader, lim limits) *lineReader {
	return &lineReader{source: bufio.NewReaderSize(r, readBufferBytes), limits: lim}
}

// next returns the next complete line. It reports false at the end of the
// input; bytes after the last newline are then left unconsumed, because the
// writer may still be appending to that line.
func (r *lineReader) next() (line, bool, error) {
	r.buf = r.buf[:0]
	var size int64
	oversized := false
	for {
		piece, err := r.source.ReadSlice('\n')
		size += int64(len(piece))
		complete := err == nil
		if complete {
			piece = piece[:len(piece)-1]
		}
		if !oversized {
			oversized = r.keep(piece)
		}
		switch {
		case complete:
			return line{data: r.buf, size: size, oversized: oversized}, true, nil
		case errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF):
			return line{}, false, nil
		default:
			return line{}, false, err
		}
	}
}

// keep appends piece to the current line and reports whether the line has
// grown past the limit. From then on only the sniff prefix is retained and
// the caller stops handing over further pieces.
func (r *lineReader) keep(piece []byte) bool {
	if len(r.buf)+len(piece) <= r.limits.line {
		r.buf = append(r.buf, piece...)
		return false
	}
	if missing := r.limits.sniff - len(r.buf); missing > 0 {
		r.buf = append(r.buf, piece[:min(missing, len(piece))]...)
	} else {
		r.buf = r.buf[:max(r.limits.sniff, 0)]
	}
	return true
}
