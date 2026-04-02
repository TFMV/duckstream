package quic

import (
	"context"

	"github.com/quic-go/quic-go"
)

type Stream struct {
	stream *quic.Stream
	id     quic.StreamID
}

func NewStream(stream *quic.Stream) *Stream {
	return &Stream{
		stream: stream,
		id:     stream.StreamID(),
	}
}

func (s *Stream) StreamID() quic.StreamID {
	return s.id
}

func (s *Stream) Read(p []byte) (int, error) {
	return (*s.stream).Read(p)
}

func (s *Stream) Write(p []byte) (int, error) {
	return (*s.stream).Write(p)
}

func (s *Stream) Close() error {
	return (*s.stream).Close()
}

func (s *Stream) Context() context.Context {
	return (*s.stream).Context()
}
