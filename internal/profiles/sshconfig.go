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
// patterns (Host *, Host !x) are skipped as entries — they set defaults,
// not connectable hosts — and what a Host * block sets (User, Port,
// IdentityFile) is applied to the hosts missing it, as ssh does. A
// missing file is an empty list, not an error.
func ReadSSHConfig(path string) []SSHConfigHost {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []SSHConfigHost
	var defaults SSHConfigHost // what the wildcard blocks set
	var cur *SSHConfigHost     // the host being read, nil outside one
	block := func() *SSHConfigHost {
		if cur != nil {
			return cur
		}
		return &defaults
	}
	flush := func() {
		if cur != nil && cur.Alias != "" {
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
			// 一行可以有多个 pattern;只收第一个具体的,全是通配就是
			// 一段默认值。
			for _, pat := range strings.Fields(val) {
				if strings.ContainsAny(pat, "*?!") {
					continue
				}
				cur = &SSHConfigHost{Alias: pat}
				break
			}
		case "hostname":
			block().HostName = val
		case "port":
			block().Port = val
		case "user":
			block().User = val
		case "identityfile":
			if b := block(); b.Identity == "" { // 只用第一把
				b.Identity = expandTilde(val)
			}
		}
	}
	flush()
	for i := range out {
		h := &out[i]
		if h.HostName == "" {
			h.HostName = h.Alias
		}
		if h.User == "" {
			h.User = defaults.User
		}
		if h.Port == "" {
			h.Port = defaults.Port
		}
		if h.Identity == "" {
			h.Identity = defaults.Identity
		}
	}
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
