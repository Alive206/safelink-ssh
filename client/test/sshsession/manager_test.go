package sshsession_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/example/safelink/client/internal/sshsession"
)

func TestManagerStreamsOutputAndControlsSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	outputReader, outputWriter := io.Pipe()
	fake := &fakePTY{}
	var outputs []sshsession.OutputEvent
	var closed []sshsession.ClosedEvent

	mgr := sshsession.NewManager(sshsession.Options{
		Open: func(context.Context, sshsession.Config) (sshsession.PTY, io.Reader, error) {
			return fake, outputReader, nil
		},
		OnOutput: func(event sshsession.OutputEvent) {
			outputs = append(outputs, event)
		},
		OnClosed: func(event sshsession.ClosedEvent) {
			closed = append(closed, event)
		},
	})

	id, err := mgr.Create(ctx, sshsession.Config{
		Addr: "example.com:22",
		User: "alice",
		Rows: 24,
		Cols: 80,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if !mgr.Active(id) {
		t.Fatalf("session %q should be active", id)
	}

	if err := mgr.Write(id, "pwd\r"); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := fake.stdin.String(); got != "pwd\r" {
		t.Fatalf("written input = %q, want pwd\\r", got)
	}

	fake.stdin.Reset()
	if err := mgr.WriteChunks(id, [][]byte{[]byte{0x1b, '['}, []byte("A")}); err != nil {
		t.Fatalf("WriteChunks returned error: %v", err)
	}
	if got := fake.stdin.String(); got != "\x1b[A" {
		t.Fatalf("written chunks = %q, want escape up sequence", got)
	}

	if err := mgr.Resize(id, 40, 120); err != nil {
		t.Fatalf("Resize returned error: %v", err)
	}
	if fake.rows != 40 || fake.cols != 120 {
		t.Fatalf("resize = %dx%d, want 40x120", fake.rows, fake.cols)
	}

	_, _ = outputWriter.Write([]byte("hello\r\n"))
	eventually(t, func() bool {
		return len(outputs) == 1 && outputs[0].SessionID == id && outputs[0].Data == "hello\r\n"
	})

	if err := mgr.Close(id); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	_ = outputWriter.Close()
	fake.finish(nil)

	eventually(t, func() bool {
		return !mgr.Active(id) && len(closed) == 1 && closed[0].SessionID == id
	})
}

func TestManagerReportsOpenError(t *testing.T) {
	want := errors.New("dial failed")
	mgr := sshsession.NewManager(sshsession.Options{
		Open: func(context.Context, sshsession.Config) (sshsession.PTY, io.Reader, error) {
			return nil, nil, want
		},
	})

	_, err := mgr.Create(context.Background(), sshsession.Config{Addr: "example.com:22", User: "alice"})
	if !errors.Is(err, want) {
		t.Fatalf("Create error = %v, want %v", err, want)
	}
}

type fakePTY struct {
	stdin  bytes.Buffer
	rows   int
	cols   int
	closed bool
	done   chan error
}

func (f *fakePTY) Write(data []byte) (int, error) {
	return f.stdin.Write(data)
}

func (f *fakePTY) Close() error {
	f.closed = true
	return nil
}

func (f *fakePTY) WindowChange(rows, cols int) error {
	f.rows = rows
	f.cols = cols
	return nil
}

func (f *fakePTY) Wait() error {
	if f.done == nil {
		f.done = make(chan error, 1)
	}
	return <-f.done
}

func (f *fakePTY) finish(err error) {
	if f.done == nil {
		f.done = make(chan error, 1)
	}
	f.done <- err
}

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition was not met before timeout")
}
