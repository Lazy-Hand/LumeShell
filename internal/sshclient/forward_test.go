package sshclient

import "testing"

func TestCloseTunnelRemovesIt(t *testing.T) {
	// 关一条隧道得把它从表里摘掉:留着的条目会让弹窗列出幽灵。
	c := &Client{}
	closed := false
	tn := &Tunnel{Kind: "local", Bind: "127.0.0.1:8080", Target: "db:5432", close: func() error {
		closed = true
		return nil
	}}
	c.tunnels().add(tn)
	if got := c.Tunnels(); len(got) != 1 || got[0] != tn {
		t.Fatalf("Tunnels = %v, want the one added", got)
	}
	c.CloseTunnel(tn)
	if !closed {
		t.Fatal("CloseTunnel did not close the listener")
	}
	if got := c.Tunnels(); len(got) != 0 {
		t.Fatalf("Tunnels = %v after CloseTunnel, want none", got)
	}
	// 再关一次不该带走别人。
	other := &Tunnel{Kind: "dynamic", Bind: "127.0.0.1:1080", close: func() error { return nil }}
	c.tunnels().add(other)
	c.CloseTunnel(tn)
	if got := c.Tunnels(); len(got) != 1 || got[0] != other {
		t.Fatalf("Tunnels = %v, want the remaining one", got)
	}
}
