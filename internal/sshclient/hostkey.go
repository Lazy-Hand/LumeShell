package sshclient

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// KnownHostsPath is the file OpenSSH records trusted host keys in.
func KnownHostsPath() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".ssh", "known_hosts")
}

// HostKeys verifies a server's key against the user's known_hosts, as ssh
// does: a host it lists with another key is refused, and a host it does
// not list is offered to ask, which decides whether the key is saved.
//
// A nil ask refuses every unknown host. ask runs on the connecting
// goroutine and may block, as a dialog does.
func HostKeys(ask func(host string, key ssh.PublicKey) bool) ssh.HostKeyCallback {
	path := KnownHostsPath()
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		// The file is read per handshake: it is small, handshakes are
		// rare, and a key trusted a moment ago must count on the next
		// connect without restarting the app.
		var check ssh.HostKeyCallback
		if path != "" {
			if c, err := knownhosts.New(path); err == nil {
				check = c // a missing or unreadable file leaves every host new
			}
		}
		if check != nil {
			err := check(hostname, remote, key)
			if err == nil {
				return nil
			}
			var changed *knownhosts.KeyError
			if !errors.As(err, &changed) {
				return err
			}
			if len(changed.Want) > 0 {
				return fmt.Errorf("%s: host key changed; remove its line from %s if that is expected: %w",
					hostname, path, err)
			}
		}
		if ask == nil || path == "" {
			return fmt.Errorf("%s: host key is not known", hostname)
		}
		if !ask(hostname, key) {
			return fmt.Errorf("%s: host key was not trusted", hostname)
		}
		return saveHostKey(path, hostname, key)
	}
}

// saveHostKey appends the host's key to the known_hosts file, which is
// what ssh does when the user answers yes.
func saveHostKey(path, hostname string, key ssh.PublicKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"
	if _, err := f.WriteString(line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
