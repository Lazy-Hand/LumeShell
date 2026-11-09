package profiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSSHConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	cfg := `
Host *
    ServerAliveInterval 30

Host dev dev2
    HostName 10.0.0.1
    User wjt
    Port 2222
    IdentityFile ~/.ssh/id_ed25519

Host prod
    HostName prod.example.com
    User=deploy
    IdentityFile /keys/prod.pem

# 注释和空行跳过
Host !bad *.wild

Host bare
`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ReadSSHConfig(path)
	if len(got) != 3 {
		t.Fatalf("want 3 hosts, got %d: %+v", len(got), got)
	}
	if got[0].Alias != "dev" || got[0].HostName != "10.0.0.1" || got[0].User != "wjt" || got[0].Port != "2222" {
		t.Fatalf("dev wrong: %+v", got[0])
	}
	if got[0].Identity == "" || got[0].Identity[:1] != "/" {
		t.Fatalf("identity not expanded: %q", got[0].Identity)
	}
	if got[1].Alias != "prod" || got[1].User != "deploy" || got[1].Identity != "/keys/prod.pem" {
		t.Fatalf("prod wrong: %+v", got[1])
	}
	if got[2].Alias != "bare" || got[2].HostName != "bare" {
		t.Fatalf("bare should take alias as hostname: %+v", got[2])
	}
}

func TestReadSSHConfigMissing(t *testing.T) {
	if got := ReadSSHConfig("/nonexistent/config"); got != nil {
		t.Fatalf("missing file should give nil, got %+v", got)
	}
}

func TestSplitKV(t *testing.T) {
	cases := []struct {
		in, k, v string
	}{
		{"Host dev", "Host", "dev"},
		{"User=deploy", "User", "deploy"},
		{"Port    2222", "Port", "2222"},
		{"HostName=a.b.c", "HostName", "a.b.c"},
		{"IdentityFile ~/a b", "IdentityFile", "~/a b"},
	}
	for _, tc := range cases {
		k, v := splitKV(tc.in)
		if k != tc.k || v != tc.v {
			t.Fatalf("%q → (%q,%q), want (%q,%q)", tc.in, k, v, tc.k, tc.v)
		}
	}
}
