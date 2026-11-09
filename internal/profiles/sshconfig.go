// sshconfig.go reads ~/.ssh/config so the host list can offer its
// entries as profiles without the user retyping them.
package profiles

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// SSHConfigHost is one `Host` block of ~/.ssh/config: the alias it
// answers to and the pieces the profile form needs.
type SSHConfigHost struct {
	Alias    string // the Host pattern, e.g. "dev"
	HostName string // HostName; the alias when none is set
	Port     string // Port; "" means the ssh default 22
	User     string
	Identity string // IdentityFile, ~ expanded; "" means agent/default keys
}

// ReadSSHConfig parses path's Host blocks. Wildcard and negated
// patterns (Host *, Host !x) are skipped — they set defaults, not
// connectable entries. A missing file is an empty list, not an error.
func ReadSSHConfig(path string) []SSHConfigHost {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []SSHConfigHost
	var cur *SSHConfigHost
	flush := func() {
		if cur != nil && cur.Alias != "" {
			if cur.HostName == "" {
				cur.HostName = cur.Alias
			}
			out = append(out, *cur)
		}
		cur = nil
	}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val := splitKV(line)
		if key == "" {
			continue
		}
		switch strings.ToLower(key) {
		case "host":
			flush()
			// 一行可以有多个 pattern;只收第一个具体的。
			for _, pat := range strings.Fields(val) {
				if strings.ContainsAny(pat, "*?!") {
					continue
				}
				cur = &SSHConfigHost{Alias: pat}
				break
			}
		case "hostname":
			if cur != nil {
				cur.HostName = val
			}
		case "port":
			if cur != nil {
				cur.Port = val
			}
		case "user":
			if cur != nil {
				cur.User = val
			}
		case "identityfile":
			if cur != nil && cur.Identity == "" { // 只用第一把
				cur.Identity = expandTilde(val)
			}
		}
	}
	flush()
	return out
}

// splitKV cuts "Key value" or "Key=value" into its halves; a line with
// no value after its key yields "".
func splitKV(line string) (key, val string) {
	if k, v, ok := strings.Cut(line, "="); ok {
		return strings.TrimSpace(k), strings.TrimSpace(v)
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		if len(parts) == 1 {
			return parts[0], ""
		}
		return "", ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}

// expandTilde replaces a leading ~ with the user's home dir.
func expandTilde(p string) string {
	if p == "~" {
		if h, err := os.UserHomeDir(); err == nil {
			return h
		}
	}
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

// DefaultSSHConfigPath is the file OpenSSH reads.
func DefaultSSHConfigPath() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".ssh", "config")
}
