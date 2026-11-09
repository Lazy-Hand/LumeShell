package sshclient

import (
	"fmt"
	"io"
	"net"
	"sync"
)

// Tunnel is one port forward running over the connection. Kind is
// "local", "remote" or "dynamic"; Bind is the address it listens on
// (this machine for local and dynamic, the server for remote) and
// Target where its traffic goes (a local listener's target for local
// and remote; dynamic's is the SOCKS address each conn asks for).
type Tunnel struct {
	Kind   string
	Bind   string
	Target string

	close func() error
}

// Close ends the listener and leaves the SSH connection up.
func (t *Tunnel) Close() error {
	if t.close != nil {
		return t.close()
	}
	return nil
}

// spec is one forward the app asked for, without its listener yet.
type spec struct {
	kind, bind, target string
}

// tunnels is the live forwards over one Client.
type tunnels struct {
	mu   sync.Mutex
	list []*Tunnel
}

func (c *Client) tunnels() *tunnels {
	if c.fwds == nil {
		c.fwds = &tunnels{}
	}
	return c.fwds
}

// Tunnels lists the forwards still running.
func (c *Client) Tunnels() []Tunnel {
	t := c.tunnels()
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Tunnel, 0, len(t.list))
	for _, tn := range t.list {
		out = append(out, *tn)
	}
	return out
}

// ForwardLocal listens on bind (e.g. "127.0.0.1:8080") here and pipes
// each conn through the SSH channel to target on the server's side
// (e.g. "db.internal:5432").
func (c *Client) ForwardLocal(bind, target string) (*Tunnel, error) {
	l, err := net.Listen("tcp", bind)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", bind, err)
	}
	tn := &Tunnel{Kind: "local", Bind: l.Addr().String(), Target: target, close: l.Close}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go c.pipeLocal(conn, target)
		}
	}()
	c.tunnels().add(tn)
	return tn, nil
}

func (c *Client) pipeLocal(local net.Conn, target string) {
	defer local.Close()
	rc, err := c.ssh.Dial("tcp", target)
	if err != nil {
		return
	}
	defer rc.Close()
	join(local, rc)
}

// ForwardRemote asks the server to listen on bind (e.g. "0.0.0.0:9000"
// or ":9000") and pipe each incoming conn back to target here (e.g.
// "127.0.0.1:3000").
func (c *Client) ForwardRemote(bind, target string) (*Tunnel, error) {
	l, err := c.ssh.Listen("tcp", bind)
	if err != nil {
		return nil, fmt.Errorf("remote listen %s: %w", bind, err)
	}
	tn := &Tunnel{Kind: "remote", Bind: l.Addr().String(), Target: target, close: l.Close}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go pipeToLocal(conn, target)
		}
	}()
	c.tunnels().add(tn)
	return tn, nil
}

func pipeToLocal(rc net.Conn, target string) {
	defer rc.Close()
	lc, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer lc.Close()
	join(rc, lc)
}

// ForwardDynamic listens on bind here and answers SOCKS5: each conn's
// destination is dialed through the SSH channel, so any app can use it
// as a proxy.
func (c *Client) ForwardDynamic(bind string) (*Tunnel, error) {
	l, err := net.Listen("tcp", bind)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", bind, err)
	}
	tn := &Tunnel{Kind: "dynamic", Bind: l.Addr().String(), close: l.Close}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go c.serveSocks(conn)
		}
	}()
	c.tunnels().add(tn)
	return tn, nil
}

// serveSocks is a minimal SOCKS5 handshake: no auth, CONNECT only.
func (c *Client) serveSocks(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 262)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil || buf[0] != 5 {
		return
	}
	n := int(buf[1])
	if _, err := io.ReadFull(conn, buf[:n]); err != nil {
		return
	}
	conn.Write([]byte{5, 0}) // no auth
	if _, err := io.ReadFull(conn, buf[:4]); err != nil || buf[0] != 5 || buf[1] != 1 {
		return
	}
	var host string
	switch buf[3] {
	case 1: // IPv4
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 3: // domain
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		if _, err := io.ReadFull(conn, buf[4:4+int(buf[0])]); err != nil {
			return
		}
		host = string(buf[4 : 4+int(buf[0])])
	case 4: // IPv6
		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return
		}
		host = net.IP(buf[:16]).String()
	default:
		return
	}
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	port := int(buf[0])<<8 | int(buf[1])
	rc, err := c.ssh.Dial("tcp", net.JoinHostPort(host, fmt.Sprint(port)))
	if err != nil {
		conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0}) // connection refused
		return
	}
	defer rc.Close()
	conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	join(conn, rc)
}

// join copies both ways until either side ends.
func join(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
}

func (t *tunnels) add(tn *Tunnel) {
	t.mu.Lock()
	t.list = append(t.list, tn)
	t.mu.Unlock()
}

// CloseTunnel removes and closes one forward of the client.
func (c *Client) CloseTunnel(tn *Tunnel) {
	t := c.tunnels()
	t.mu.Lock()
	for i, x := range t.list {
		if x == tn {
			t.list = append(t.list[:i], t.list[i+1:]...)
			break
		}
	}
	t.mu.Unlock()
	tn.Close()
}

// CloseTunnels ends every forward of the client.
func (c *Client) CloseTunnels() {
	t := c.tunnels()
	t.mu.Lock()
	list := t.list
	t.list = nil
	t.mu.Unlock()
	for _, tn := range list {
		tn.Close()
	}
}

// the tunnels registry lives on the Client (client.go); nothing else
// needs an ssh import here
