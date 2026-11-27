package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestProcessWriterStopsOnFullPipe(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	defer reader.Close()
	release := time.AfterFunc(5*time.Second, func() { reader.Close() })
	defer release.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = (processWriter{writer}).WriteContext(ctx, bytes.Repeat([]byte("x"), 4<<20))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write to full pipe: %v", err)
	}
}

func TestProcessWriterTransfersLargeOutput(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	want := bytes.Repeat([]byte("some text\n"), 30000)
	n, err := (processWriter{file}).WriteContext(context.Background(), want)
	if err != nil || n != len(want) {
		t.Fatalf("wrote %d bytes, want %d: %v", n, len(want), err)
	}
	got, err := os.ReadFile(file.Name())
	if err != nil || !bytes.Equal(want, got) {
		t.Fatalf("read back %d bytes, want original content: %v", len(got), err)
	}
}
