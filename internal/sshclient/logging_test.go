package sshclient

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSessionLogsNeverMix(t *testing.T) {
	// 同一秒里开两个会话(或两次启动同一个主机)各写各的文件,
	// 不能像以前那样 O_APPEND 到同一个文件里。
	dir := t.TempDir()
	a := OpenSessionLogger(dir, "host")
	b := OpenSessionLogger(dir, "host")
	ap, bp := a.Path(), b.Path()
	if ap == "" || bp == "" {
		t.Fatal("a logger recorded nothing")
	}
	if ap == bp {
		t.Fatalf("both sessions write %s", ap)
	}
	a.Write([]byte("alpha"))
	b.Write([]byte("beta"))
	a.Close()
	b.Close()
	if got := readFile(t, ap); !bytes.Contains(got, []byte("alpha")) || bytes.Contains(got, []byte("beta")) {
		t.Fatalf("%s holds %q", ap, got)
	}
	if got := readFile(t, bp); !bytes.Contains(got, []byte("beta")) || bytes.Contains(got, []byte("alpha")) {
		t.Fatalf("%s holds %q", bp, got)
	}
}

func TestSessionLogRotates(t *testing.T) {
	dir := t.TempDir()
	l := OpenSessionLogger(dir, "chatty")
	first := l.Path()
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for written := int64(0); written < 2*maxLogBytes; written += int64(len(chunk)) {
		l.Write(chunk)
	}
	second := l.Path()
	l.Close()
	if second == "" {
		t.Fatal("logging stopped instead of rotating")
	}
	if second == first {
		t.Fatalf("the log grew past its cap without a new part: %s", first)
	}
	if st, err := os.Stat(second); err != nil || st.Size() == 0 {
		t.Fatalf("the new part is missing or empty: %v", err)
	}
	if !strings.Contains(second, "-") || first == second {
		t.Fatalf("parts = %s, %s, want two of one name", first, second)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
