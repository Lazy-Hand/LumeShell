package filepanel

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 两列浏览器的测试:左列本机文件,右列同一棵本机树充作"服务器",
// 传输就是本机复制,全部状态机照真走。

func waitBoth(t *testing.T, changed chan struct{}, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-changed:
		case <-time.After(time.Second):
			t.Fatalf("only %d of %d listings came", i, n)
		}
	}
}

func TestTwoColumnTransfer(t *testing.T) {
	homeL := t.TempDir()
	homeR := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeL, "send.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := New(LocalFS{}, homeL)
	remote := New(&fakeFS{root: homeR}, homeR)
	changed := make(chan struct{}, 16)
	local.SetOther(remote, func() { changed <- struct{}{} })
	remote.SetOther(local, func() { changed <- struct{}{} })
	local.Open(homeL, func() { changed <- struct{}{} })
	remote.Open(homeR, func() { changed <- struct{}{} })
	waitBoth(t, changed, 2)

	// 左列把 send.txt 发去右列的目录:进度报告会先走几个 changed,
	// 右列的 kick 收尾;等文件出现为止。
	go local.sendOut("send.txt")
	deadline := time.Now().Add(2 * time.Second)
	var data []byte
	for {
		var err error
		data, err = os.ReadFile(filepath.Join(homeR, "send.txt"))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the file did not arrive: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if string(data) != "payload" {
		t.Fatalf("arrived %q", data)
	}
	// 传输的进度条在右列:send.txt 的条目带着完成态。
	sends := remote.Sends()
	if len(sends) != 1 || sends[0].name != "send.txt" || sends[0].err != "" || sends[0].done != sends[0].total {
		t.Fatalf("sends = %+v, want one finished send.txt", sends)
	}
}

func TestTwoColumnNavigateIndependently(t *testing.T) {
	homeL := t.TempDir()
	homeR := t.TempDir()
	if err := os.Mkdir(filepath.Join(homeL, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(homeR, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	local := New(LocalFS{}, homeL)
	remote := New(&fakeFS{root: homeR}, homeR)
	changed := make(chan struct{}, 16)
	local.Open(homeL, func() { changed <- struct{}{} })
	remote.Open(homeR, func() { changed <- struct{}{} })
	waitBoth(t, changed, 2)

	local.Visit("docs", func() { changed <- struct{}{} })
	waitBoth(t, changed, 1)
	if local.Dir() != filepath.Join(homeL, "docs") {
		t.Fatalf("left at %q", local.Dir())
	}
	if remote.Dir() != homeR {
		t.Fatalf("right moved to %q with the left", remote.Dir())
	}
	remote.Visit("logs", func() { changed <- struct{}{} })
	waitBoth(t, changed, 1)
	if local.Dir() != filepath.Join(homeL, "docs") {
		t.Fatalf("left moved with the right: %q", local.Dir())
	}
}

func TestSendWithoutOtherSavesToPick(t *testing.T) {
	// 没有对列的单列面板退回到保存对话框;这里只验证不炸、不留文件。
	home := t.TempDir()
	p := New(LocalFS{}, home)
	if err := os.WriteFile(filepath.Join(home, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.sendOut("a.txt") // 弹窗在别的 goroutine 起;测试不点它。
}
