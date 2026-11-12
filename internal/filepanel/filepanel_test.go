package filepanel

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lumeshell/internal/sshclient"
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
			Name:    e.Name(),
			Size:    info.Size(),
			Mode:    info.Mode(),
			ModTime: info.ModTime(),
			IsDir:   e.IsDir(),
		})
	}
	return out, nil
}

func (f *fakeFS) Mkdir(dir string) error                     { return os.Mkdir(dir, 0o755) }
func (f *fakeFS) Join(dir, name string) string               { return filepath.Join(dir, name) }
func (f *fakeFS) Remove(p string) error                      { return os.Remove(p) }
func (f *fakeFS) Rename(from, to string) error               { return os.Rename(from, to) }
func (f *fakeFS) Create(path string) (io.WriteCloser, error) { return os.Create(path) }
func (f *fakeFS) Close() error                               { return nil }

// Send 流式复制本机文件到对面通道的 writer:测试里对面也是本机,进度
// 随拷贝的字节报出。
func (f *fakeFS) Send(src, remote string, dst sshclient.WriterFor, report func(done, total int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	var total int64
	if report != nil {
		if st, err := in.Stat(); err == nil {
			total = st.Size()
		}
	}
	out, err := dst.Create(remote)
	if err != nil {
		return err
	}
	buf := make([]byte, 32*1024)
	var done int64
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				return werr
			}
			done += int64(n)
			if report != nil {
				report(done, total)
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			out.Close()
			return rerr
		}
	}
	return out.Close()
}

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

	// Mkdir 走暂存的名字,由对话框的确定带进来。
	p.newDir = "新建"
	p.Mkdir(func() { changed <- struct{}{} })
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

func TestRename(t *testing.T) {
	p, home := newPanel(t)
	changed := make(chan struct{}, 8)
	if err := os.WriteFile(home+"/old.txt", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.Open(home, func() { changed <- struct{}{} })
	waitChanged(t, changed)

	// 重命名走对话框的暂存:from 来自右键的行,to 是输入的名字。
	p.rename, p.renameTo = "old.txt", "new.txt"
	p.DoRename(func() { changed <- struct{}{} })
	waitChanged(t, changed)
	if _, err := os.Stat(home + "/new.txt"); err != nil {
		t.Fatalf("renamed file not there: %v", err)
	}
	if _, err := os.Stat(home + "/old.txt"); !os.IsNotExist(err) {
		t.Fatal("old.txt still there")
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

// delayedFS slows one directory's listing, so an older Open can land
// after a newer one.
type delayedFS struct {
	*fakeFS
	slow string
	wait time.Duration
}

func (f *delayedFS) ReadDir(dir string) ([]sshclient.Entry, error) {
	if dir == f.slow {
		time.Sleep(f.wait)
	}
	return f.fakeFS.ReadDir(dir)
}

func TestStaleListingDoesNotWin(t *testing.T) {
	// 慢目录的列表比后点的目录晚回来时,不能把面板翻回去。
	home := t.TempDir()
	for _, d := range []string{"slow", "fast"} {
		if err := os.Mkdir(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "fast", "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := &delayedFS{fakeFS: &fakeFS{root: home}, slow: filepath.Join(home, "slow"), wait: 150 * time.Millisecond}
	p := New(fs, home)
	changed := make(chan struct{}, 8)
	p.Open(filepath.Join(home, "slow"), func() { changed <- struct{}{} })
	p.Open(filepath.Join(home, "fast"), func() { changed <- struct{}{} })
	waitChanged(t, changed)
	if p.Dir() != filepath.Join(home, "fast") {
		t.Fatalf("dir = %q", p.Dir())
	}
	select {
	case <-changed: // the slow listing came back and was dropped
	case <-time.After(500 * time.Millisecond):
	}
	if p.Dir() != filepath.Join(home, "fast") {
		t.Fatalf("a stale listing moved the panel to %q", p.Dir())
	}
	if ents := p.Entries(); len(ents) != 1 || ents[0].Name != "f.txt" {
		t.Fatalf("entries = %+v, want fast's", ents)
	}
}
