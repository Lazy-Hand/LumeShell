package main

// main 没有可独立单测的状态:列表页与表单在 internal/appui 里测,
// 网络部分在 internal/sshclient 里测。这里只测会话表的顺序,它是
// 纯数据。

import (
	"fmt"
	"testing"
)

func newSessions(seqs ...int) *app {
	a := &app{sess: map[string]*session{}}
	for _, seq := range seqs {
		ss := &session{id: fmt.Sprintf("s%d", seq), seq: seq}
		a.sess[ss.id] = ss
	}
	return a
}

func TestTabsKeepOpeningOrder(t *testing.T) {
	a := newSessions(3, 1, 2)
	got, _ := a.orderedSessions()
	if len(got) != 3 || got[0].seq != 1 || got[1].seq != 2 || got[2].seq != 3 {
		t.Fatalf("order = %v, want 1,2,3", []int{got[0].seq, got[1].seq, got[2].seq})
	}
}

func TestNextTabIsTheNeighbour(t *testing.T) {
	// 关掉中间那个,该落到右边相邻的;关掉最后那个,回到前一个。
	a := newSessions(1, 2, 3)
	if got := a.nextTabLocked(2); got != "s3" {
		t.Fatalf("closing the middle picked %q, want s3", got)
	}
	if got := a.nextTabLocked(3); got != "s2" {
		t.Fatalf("closing the last picked %q, want s2", got)
	}
	if got := a.nextTabLocked(1); got != "s2" {
		t.Fatalf("closing the first picked %q, want s2", got)
	}
}
