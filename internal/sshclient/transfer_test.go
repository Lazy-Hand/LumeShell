package sshclient

import (
	"bytes"
	"crypto/rand"
	"io"
	"sync"
	"testing"
)

// memFile is an in-memory ReaderAt/WriterAt pair for SendChunked.
type memFile struct {
	mu sync.Mutex
	b  []byte
}

func (m *memFile) ReadAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return bytes.NewReader(m.b[off:]).Read(p)
}

func (m *memFile) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if need := int(off) + len(p); need > len(m.b) {
		m.b = append(m.b, make([]byte, need-len(m.b))...)
	}
	return copy(m.b[off:], p), nil
}

func TestSendChunkedCopiesEveryByte(t *testing.T) {
	total := int64(chunkThreshold + chunkSize + 12345) // 三块多,进分块路径
	src := &memFile{b: make([]byte, total)}
	if _, err := rand.Read(src.b); err != nil {
		t.Fatal(err)
	}
	dst := &memFile{}

	var last int64
	report := func(done, tot int64) {
		if done > last {
			last = done
		}
		if tot != total {
			t.Errorf("total = %d, want %d", tot, total)
		}
	}
	if err := SendChunked(src, dst, total, report); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src.b, dst.b) {
		t.Fatal("the copy does not match")
	}
	if last != total {
		t.Fatalf("report stopped at %d of %d", last, total)
	}
}

func TestSendChunkedSmallFile(t *testing.T) {
	src := &memFile{b: []byte("hello world")}
	dst := &memFile{}
	if err := SendChunked(src, dst, int64(len(src.b)), nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src.b, dst.b) {
		t.Fatalf("copied %q", dst.b)
	}
}

// errWriter fails every WriteAt, to see SendChunked surface it.
type errWriter struct{ err error }

func (w errWriter) WriteAt([]byte, int64) (int, error) { return 0, w.err }

func TestSendChunkedReportsWriteFailure(t *testing.T) {
	src := &memFile{b: make([]byte, chunkSize)}
	if err := SendChunked(src, errWriter{io.ErrClosedPipe}, chunkSize, nil); err == nil {
		t.Fatal("no error for a writer that fails")
	}
}
