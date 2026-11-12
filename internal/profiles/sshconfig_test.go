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

func TestSSHConfigWildcardDefaults(t *testing.T) {
	// Host * 里的 User/Port/IdentityFile 是默认值,像 ssh 那样落到
	// 没写这几项的主机上。
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	cfg := `
Host *
    User shared
    Port 2200
    IdentityFile /keys/default

Host app
    HostName app.example.com

Host db
    HostName db.example.com
    User dbadmin
`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	got := ReadSSHConfig(path)
	if len(got) != 2 {
		t.Fatalf("want 2 hosts, got %+v", got)
	}
	app, db := got[0], got[1]
	if app.User != "shared" || app.Port != "2200" || app.Identity != "/keys/default" {
		t.Fatalf("app took no defaults: %+v", app)
	}
	if db.User != "dbadmin" || db.Port != "2200" {
		t.Fatalf("db lost its own user or the default port: %+v", db)
	}
}
