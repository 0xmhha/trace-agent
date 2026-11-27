package timeline

import (
	"bufio"
	"errors"
	"io"
)

// readBufferBytes is the size of the buffer lines are read through. Lines
// that fit in it are handed out without being copied.
const readBufferBytes = 64 << 10

// line is one transcript line without its terminator. data is valid until
// the next call of lineReader.next and is nil for an oversized line.
type line struct {
	data       []byte
	start      int64
	oversized  bool
	terminated bool
}

// lineReader splits a transcript into lines and tracks their byte offsets.
// A line longer than max is consumed without being kept in memory.
type lineReader struct {
	br  *bufio.Reader
	off int64
	max int
	buf []byte
}

func newLineReader(r io.Reader, offset int64, maxLine int) *lineReader {
	return &lineReader{br: bufio.NewReaderSize(r, readBufferBytes), off: offset, max: maxLine}
}

// next returns the next line, or io.EOF when no byte is left. A last line
// without a newline is returned with terminated unset.
func (l *lineReader) next() (line, error) {
	ln := line{start: l.off}
	l.buf = l.buf[:0]
	for {
		chunk, err := l.br.ReadSlice('\n')
		l.off += int64(len(chunk))
		if n := len(chunk); n > 0 && chunk[n-1] == '\n' {
			ln.terminated = true
			chunk = chunk[:n-1]
		}
		switch {
		case ln.oversized:
		case len(l.buf)+len(chunk) > l.max:
			ln.oversized = true
			// Drop what was collected so far instead of keeping the
			// oversized prefix alive for the rest of the read.
			l.buf = nil
		case err == nil && len(l.buf) == 0:
			ln.data = chunk
			return ln, nil
		default:
			l.buf = append(l.buf, chunk...)
		}
		switch {
		case err == nil:
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if l.off == ln.start {
				return line{}, io.EOF
			}
		default:
			return line{}, err
		}
		if !ln.oversized {
			ln.data = l.buf
		}
		return ln, nil
	}
}
