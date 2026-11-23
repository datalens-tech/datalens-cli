package main

import (
	"context"
	"os"
)

type processWriter struct {
	*os.File
}

func (w processWriter) WriteContext(ctx context.Context, data []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)
	// An inherited pipe can block until process exit, so a pending write must own its bytes.
	buf := make([]byte, min(len(data), 64<<10))
	var written int
	for written < len(data) {
		if err := context.Cause(ctx); err != nil {
			return written, err
		}
		n := copy(buf, data[written:])
		go func() {
			nw, err := w.File.Write(buf[:n])
			done <- result{nw, err}
		}()
		select {
		case r := <-done:
			written += r.n
			if r.err != nil {
				return written, r.err
			}
		case <-ctx.Done():
			return written, context.Cause(ctx)
		}
	}
	return written, nil
}
