package service

import (
	"context"
	"io"
)

// ctxReader makes a transcript reader honour the request context. The
// TimelineFunc takes no context, so without it a page request over a huge
// transcript would keep reading after the request's deadline and hold a
// timeline slot. Every Read checks the context first and fails with its
// error once the context is done; Seek is passed through.
type ctxReader struct {
	// done is the context's Err method; the reader keeps the check rather
	// than the context itself.
	done func() error
	r    io.ReadSeeker
}

func newCtxReader(ctx context.Context, r io.ReadSeeker) ctxReader {
	return ctxReader{done: ctx.Err, r: r}
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.done(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func (c ctxReader) Seek(offset int64, whence int) (int64, error) {
	return c.r.Seek(offset, whence)
}
