package profiles

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreEmpty(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List = %d profiles, want none", len(got))
	}
}

func TestSaveAndList(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Host: "example.com", User: "root", Auth: Password})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" {
		t.Fatal("Save gives no ID")
	}
	if p.Label != "example.com" {
		t.Fatalf("Label = %q", p.Label)
	}
	if got := s.List(); len(got) != 1 || got[0].Host != "example.com" {
		t.Fatalf("List = %+v", got)
	}
}

func TestSVOrderAfterBothUsed(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Save(Profile{Host: "a", User: "u", Auth: Password})
	time.Sleep(2 * time.Millisecond)
	s.Save(Profile{Host: "b", User: "u", Auth: Password})
	got := s.List()
	if got[0].Host != "b" || got[1].Host != "a" {
		t.Fatalf("List order = [%s %s], want [b a]", got[0].Host, got[1].Host)
	}
	if err := s.Touch(a.ID); err != nil {
		t.Fatal(err)
	}
	got = s.List()
	if got[0].Host != "a" {
		t.Fatalf("after Touch, List[0] = %s, want a", got[0].Host)
	}
}

func TestIncompleteRejected(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(Profile{User: "u", Auth: Password}); err == nil {
		t.Fatal("profile without host was accepted")
	}
	if _, err := s.Save(Profile{Host: "h", User: "u", Auth: Key}); err == nil {
		t.Fatal("key profile without a key path was accepted")
	}
}

func TestNoProfileIsDropped(t *testing.T) {
	// 保存的主机都留着:静默淘汰会删掉用户的配置。
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		time.Sleep(time.Millisecond)
		if _, err := s.Save(Profile{Host: string(rune('a' + i)), User: "u", Auth: Password}); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.List(); len(got) != 25 {
		t.Fatalf("List = %d profiles, want 25", len(got))
	}
}

func TestPersistAndReload(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Label: "lab", Host: "10.0.0.1", Port: "2222", User: "deploy", Auth: Key, KeyPath: "/id", HasPass: true})
	if err != nil {
		t.Fatal(err)
	}
	// The file must hold no secret; profiles hold none, so check the
	// round trip instead.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.List()
	if len(got) != 1 {
		t.Fatalf("reloaded %d profiles, want 1", len(got))
	}
	g := got[0]
	if g.ID != p.ID || g.Host != "10.0.0.1" || g.Port != "2222" || g.User != "deploy" || g.Auth != Key || g.KeyPath != "/id" || !g.HasPass {
		t.Fatalf("reloaded = %+v", g)
	}
	if g.CreatedAt.IsZero() {
		t.Fatal("CreatedAt lost")
	}
}

func TestCorruptFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "profiles.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open of a broken file failed: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List = %d profiles from a broken file", len(got))
	}
}

func TestRemove(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Save(Profile{Host: "h", User: "u", Auth: Password})
	if err := s.Remove(p.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("List = %d profiles after Remove", len(got))
	}
}

func TestRecencySurvivesReload(t *testing.T) {
	// 重启后列表仍按最后使用排序:LastUsed 得从磁盘回来,不能拿
	// CreatedAt 顶替。
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Save(Profile{Host: "a", User: "u", Auth: Password})
	time.Sleep(2 * time.Millisecond)
	s.Save(Profile{Host: "b", User: "u", Auth: Password})
	time.Sleep(2 * time.Millisecond)
	if err := s.Touch(a.ID); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.List()
	if len(got) != 2 || got[0].Host != "a" {
		t.Fatalf("List after reload = %+v, want a first", got)
	}
}
