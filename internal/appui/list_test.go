package appui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"lume/internal/profiles"
)

func uiTester(view func(c *ui.Context)) *ui.Tester {
	return ui.NewTester(view, 960, 640)
}

func newList(t *testing.T) (*List, *profiles.Store, *connectSpy) {
	t.Helper()
	s, err := profiles.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l := NewList(s)
	spy := &connectSpy{list: l}
	l.Connect = spy.connect
	l.Delete = func(p profiles.Profile) {
		spy.deleted = append(spy.deleted, p.ID)
		_ = s.Remove(p.ID) // main 里删除就是从商店移除
	}
	return l, s, spy
}

// connectSpy records what the page asked main to do.
type connectSpy struct {
	list     *List
	profiles []profiles.Profile
	secrets  []Secret
	deleted  []string
}

func (c *connectSpy) connect(p profiles.Profile, s Secret) {
	c.profiles = append(c.profiles, p)
	c.secrets = append(c.secrets, s)
}

func TestCardsShowSavedHosts(t *testing.T) {
	l, s, spy := newList(t)
	if _, err := s.Save(profiles.Profile{Host: "example.com", User: "root", Auth: profiles.Password}); err != nil {
		t.Fatal(err)
	}
	tt := uiTester(l.View)
	if !tt.HasText("example.com") || !tt.HasText("root@example.com:22") {
		t.Errorf("texts %q lack the host", tt.Texts())
	}
	// 卡片的连接进表单:密语不入盘,卡片带不了它。
	if err := tt.Click("连接"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("登录密码"); err != nil {
		t.Fatal(err)
	}
	tt.Type("secret")
	if err := tt.Click("连接"); err != nil {
		t.Fatal(err)
	}
	if len(spy.profiles) != 1 || spy.profiles[0].Host != "example.com" {
		t.Errorf("Connect ran %+v, want the saved profile", spy.profiles)
	}
	if len(spy.secrets) != 1 || spy.secrets[0].Password != "secret" {
		t.Errorf("secrets = %+v, want the typed password", spy.secrets)
	}
}

func TestCardErrorShowsWithoutDialog(t *testing.T) {
	l, s, _ := newList(t)
	s.Save(profiles.Profile{Host: "h", User: "u", Auth: profiles.Password})
	l.FailedCard("connect h:22: refused")
	tt := uiTester(l.View)
	if !tt.HasText("connect h:22: refused") {
		t.Errorf("texts %q lack the failure", tt.Texts())
	}
	if err := tt.Click("知道了"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("connect h:22: refused") {
		t.Error("the failure stayed after dismiss")
	}
}

func TestDeleteRemovesCard(t *testing.T) {
	l, s, spy := newList(t)
	saved, _ := s.Save(profiles.Profile{Host: "h", User: "u", Auth: profiles.Password})
	tt := uiTester(l.View)
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if len(spy.deleted) != 1 || spy.deleted[0] != saved.ID {
		t.Fatalf("deleted = %v, want the card's profile", spy.deleted)
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("store still holds %d profiles", len(got))
	}
}

func TestFormConnectsAndSaves(t *testing.T) {
	l, s, spy := newList(t)
	tt := uiTester(l.View)
	if err := tt.Click("新建连接"); err != nil {
		t.Fatal(err)
	}
	// Field 的标签可点击并聚焦它的控件。
	if err := tt.Click("主机"); err != nil {
		t.Fatal(err)
	}
	tt.Type("example.com")
	if err := tt.Click("用户名"); err != nil {
		t.Fatal(err)
	}
	tt.Type("root")
	if err := tt.Click("连接"); err != nil {
		t.Fatal(err)
	}
	got := s.List()
	if len(got) != 1 || got[0].Host != "example.com" || got[0].User != "root" {
		t.Fatalf("store = %+v, want the new host", got)
	}
	if len(spy.profiles) != 1 {
		t.Fatalf("Connect ran %d times", len(spy.profiles))
	}
}

func TestFormRejectsEmptyHost(t *testing.T) {
	l, s, spy := newList(t)
	tt := uiTester(l.View)
	if err := tt.Click("新建连接"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("连接"); err != nil {
		t.Fatal(err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("store took an incomplete form: %+v", got)
	}
	if len(spy.profiles) != 0 {
		t.Fatal("Connect ran for an incomplete form")
	}
	if !tt.HasText("no host") {
		t.Errorf("texts %q lack the error", tt.Texts())
	}
}
