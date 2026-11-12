package sshclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"testing"

	"golang.org/x/crypto/ssh"
)

// testAddr is the address a handshake passes: knownhosts reads it for
// the IP entries of the file.
type testAddr string

func (a testAddr) Network() string { return "tcp" }
func (a testAddr) String() string  { return string(a) }

// testKey is a fresh host key, as a server presents one.
func testKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer.PublicKey()
}

func TestHostKeysTrustsWhatTheUserAccepts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key := testKey(t)

	asked := 0
	first := HostKeys(func(host string, k ssh.PublicKey) bool {
		asked++
		return true
	})
	if err := first("example.com:22", testAddr("10.0.0.1:22"), key); err != nil {
		t.Fatalf("a trusted new host was refused: %v", err)
	}
	if asked != 1 {
		t.Fatalf("asked %d times, want once", asked)
	}
	if _, err := os.Stat(KnownHostsPath()); err != nil {
		t.Fatalf("the key was not saved: %v", err)
	}

	// 同一个 callback 实例再连一次(重连就是这样):键已经存盘,
	// 不该再问一遍。
	if err := first("example.com:22", testAddr("10.0.0.1:22"), key); err != nil {
		t.Fatalf("a known host was refused: %v", err)
	}
	if asked != 1 {
		t.Fatalf("asked %d times, want the known host to go unasked", asked)
	}
}

func TestHostKeysRefuses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	key := testKey(t)

	cb := HostKeys(func(string, ssh.PublicKey) bool { return false })
	if err := cb("example.com:22", testAddr("10.0.0.1:22"), key); err == nil {
		t.Fatal("a refused host key was accepted")
	}

	// 已信任的主机换了键:拒绝,并且不问用户。
	if err := HostKeys(func(string, ssh.PublicKey) bool { return true })("example.com:22", testAddr("10.0.0.1:22"), key); err != nil {
		t.Fatal(err)
	}
	other := testKey(t)
	asked := false
	changed := HostKeys(func(string, ssh.PublicKey) bool {
		asked = true
		return true
	})
	if err := changed("example.com:22", testAddr("10.0.0.1:22"), other); err == nil {
		t.Fatal("a changed host key was accepted")
	}
	if asked {
		t.Fatal("a changed host key was offered instead of refused")
	}
}
