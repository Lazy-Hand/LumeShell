package filepanel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"lume/internal/sshclient"
)

// fakeFS is the panel's file channel over a local tree, serving the
// same methods the real SFTP serves, so the panel's states test without
// a server.
type fakeFS struct {
	root string
}

func (f *fakeFS) ReadDir(dir string) ([]sshclient.Entry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]sshclient.Entry, 0, len(ents))
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, sshclient.Entry{
			Name:  e.Name(),
			Size:  info.Size(),
			Mode:  info.Mode(),
			Moded: info.ModTime(),
			IsDir: e.IsDir(),
		})
	}
	return out, nil
}

func (f *fakeFS) Mkdir(dir string) error       { return os.Mkdir(dir, 0o755) }
func (f *fakeFS) Remove(p string) error        { return os.Remove(p) }
func (f *fakeFS) Download(_, _ string) error   { return nil }
func (f *fakeFS) Upload(_, _ string) error     { return nil }
func (f *fakeFS) Close() error { return nil }

// newPanel is a panel over a fresh local tree, its listing not loaded.
func newPanel(t *testing.T) (*Panel, string) {
	t.Helper()
	f := &fakeFS{root: t.TempDir()}
	return New(f, f.root), f.root
}

func writeTree(t *testing.T, root string) {
	t.Helper()
	write := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("readme.md", "hi")
	write("logs/a.log", "a")
	write("logs/b.log", "b")
	if err := os.Mkdir(root+"/empty", 0o755); err != nil {
		t.Fatal(err)
	}
}

func waitChanged(t *testing.T, changed chan struct{}) {
	t.Helper()
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("the panel never reported its list")
	}
}

func TestOpenLoadsListingDirsFirst(t *testing.T) {
	p, home := newPanel(t)
	writeTree(t, home)
	changed := make(chan struct{}, 8)
	p.Open(home, func() { changed <- struct{}{} })
	waitChanged(t, changed)
	if p.Loading() {
		t.Fatal("still loading after the listing")
	}
	if p.Err() != "" {
		t.Fatalf("err = %q", p.Err())
	}
	ents := p.Entries()
	if len(ents) != 3 { // 根下:empty/, logs/, readme.md
		t.Fatalf("%d entries at the root, want 3", len(ents))
	}
	dirs := 0
	for i, e := range ents {
		if e.IsDir {
			dirs++
		} else if i < dirs {
			t.Fatalf("file %s shows before a directory", e.Name)
		}
	}
	if dirs != 2 {
		t.Fatalf("%d dirs, want 2", dirs)
	}
}

func TestVisitAndParent(t *testing.T) {
	p, home := newPanel(t)
	writeTree(t, home)
	changed := make(chan struct{}, 8)
	p.Open(home, func() { changed <- struct{}{} })
	waitChanged(t, changed)

	p.Visit("logs", func() { changed <- struct{}{} })
	waitChanged(t, changed)
	if p.Dir() != home+"/logs" {
		t.Fatalf("dir = %q", p.Dir())
	}
	ents := p.Entries()
	if len(ents) != 2 || ents[0].Name != "a.log" || ents[1].Name != "b.log" {
		t.Fatalf("entries = %+v", ents)
	}

	p.Parent(func() { changed <- struct{}{} })
	waitChanged(t, changed)
	if p.Dir() != home {
		t.Fatalf("after Parent dir = %q", p.Dir())
	}
	// 根再往上是原地不动。
	p.Parent(func() { changed <- struct{}{} })
	if p.Dir() != home {
		t.Fatalf("Parent above the root moved to %q", p.Dir())
	}
}

func TestMkdirAndRemove(t *testing.T) {
	p, home := newPanel(t)
	changed := make(chan struct{}, 8)
	said := make(chan string, 8)
	say := func(title, _ string) { said <- title }

	p.Mkdir("新建", func() { changed <- struct{}{} }, say)
	waitChanged(t, changed)
	if _, err := os.Stat(home + "/新建"); err != nil {
		t.Fatal("the directory is not there")
	}
	select {
	case msg := <-said:
		t.Fatalf("Mkdir said %q", msg)
	default:
	}

	p.Remove("新建", func() { changed <- struct{}{} }, say)
	waitChanged(t, changed)
	if _, err := os.Stat(home + "/新建"); !os.IsNotExist(err) {
		t.Fatal("the directory is still there")
	}
}

func TestRemoveFailureSaysSo(t *testing.T) {
	p, _ := newPanel(t)
	said := make(chan string, 8)
	say := func(title, body string) { said <- title + ": " + body }
	p.Remove("没有的", func() {}, say)
	select {
	case msg := <-said:
		if msg == "" {
			t.Fatal("empty message")
		}
	case <-time.After(time.Second):
		t.Fatal("no failure message")
	}
}

func TestErrorsReachThePanel(t *testing.T) {
	p, home := newPanel(t)
	changed := make(chan struct{}, 8)
	p.Open(home+"/没有的", func() { changed <- struct{}{} })
	waitChanged(t, changed)
	if p.Err() == "" {
		t.Fatal("no error for a missing directory")
	}
	if len(p.Entries()) != 0 {
		t.Fatal("entries from a failed listing")
	}
}
