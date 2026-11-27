package cli

import (
	"context"
	"io"
)

type commandStream struct {
	ctx  context.Context
	dest io.Writer
	err  error
}

func (s *commandStream) Write(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	var n int
	if writer, ok := s.dest.(interface {
		WriteContext(context.Context, []byte) (int, error)
	}); ok {
		n, s.err = writer.WriteContext(s.ctx, p)
	} else {
		n, s.err = s.dest.Write(p)
	}
	if n != len(p) && s.err == nil {
		s.err = io.ErrShortWrite
	}
	return n, s.err
}
