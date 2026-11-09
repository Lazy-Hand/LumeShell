// Package appui builds the app's pages: the list of saved hosts and the
// form a new connection starts from. It holds no connections; main.go
// makes them and tells this package what happened.
package appui

import (
	"fmt"

	"lume/internal/profiles"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// List shows the saved hosts and takes a new connection's settings.
// Clicking a card or the connect button calls Connect, which main wires
// to actually connecting, so the page stays free of network code.
type List struct {
	store *profiles.Store

	// Connect runs with the profile of a card, or the form's new
	// settings as nil, when the user wants to connect. Delete runs with
	// the profile to drop. Both come from main.
	Connect func(p profiles.Profile, secret Secret)
	Delete  func(p profiles.Profile)

	// Win is the window the pages show in, for dialogs and redraws.
	Win *mygo.Window

	// form is the new connection the form fills in; the secret lives
	// only here, never in a profile.
	form struct {
		open     bool
		label    string
		host     string
		port     string
		user     string
		auth     int // 0 密码, 1 私钥 (Segmented takes an index)
		password string
		keyPath  string
		passphr  string
		choosing bool   // a file dialog is up
		editing  string // the profile being connected, empty for a new one
	}
	err     string // the form's error, from its last connect attempt
	cardErr string // what the last connect attempt failed with, for the cards
}

// Secret is what the form gathered to prove the user's identity.
type Secret struct {
	Password   string
	KeyPath    string
	Passphrase string
}

// NewList builds the list page over a store.
func NewList(store *profiles.Store) *List {
	l := &List{store: store}
	l.Connect = func(profiles.Profile, Secret) {}
	l.Delete = func(profiles.Profile) {}
	return l
}

// ShowHome returns the list page to its cards.
func (l *List) ShowHome() { l.form.open = false }

// FailedCard reports why a connect attempt failed, beside the cards.
func (l *List) FailedCard(why string) { l.cardErr = why }

// View builds the page.
func (l *List) View(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(32).Gap(20).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Lume").FontSize(28).Bold()
			ui.Text(c, "SSH 终端").FontSize(14).TextColor(t.TextMuted)
		})
		if l.form.open {
			l.formView(c)
		} else {
			l.cardsView(c)
		}
	})
}

// cardsView shows the saved hosts, most recently used first, and the
// button that opens the form. A card's 连接 never carries a secret:
// secrets stay out of the store, so it fills the form with the host and
// the user types the password or the key's passphrase there.
func (l *List) cardsView(c *ui.Context) {
	t := c.Theme()
	ps := l.store.List()
	if len(ps) == 0 {
		ui.Column(c).Gap(10).AlignItems(ui.Center).Padding(40).Children(func() {
			ui.Text(c, "暂无保存的主机").TextColor(t.TextMuted)
		})
	}
	if ui.PrimaryButton(c, "新建连接").Clicked() {
		l.editForm(profiles.Profile{}, false)
	}
	if l.cardErr != "" {
		ui.Row(c).FillWidth().Width(560).Gap(10).Padding(10, 12).Background(t.Surface).Radius(t.Radius).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "⚠ "+l.cardErr).FontSize(13).TextColor(t.Danger).Grow(1)
			if ui.Button(c, "知道了").Clicked() {
				l.cardErr = ""
			}
		})
	}
	ui.Column(c).Gap(12).FillWidth().Width(560).Children(func() {
		for _, p := range ps {
			profile := p
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Padding(14).Background(t.Surface).Radius(t.Radius).Children(func() {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, profile.Title()).Bold()
					ui.Text(c, profile.Endpoint()).FontSize(12).TextColor(t.TextMuted)
					sub := ""
					switch {
					case profile.Auth == profiles.Key:
						sub = "私钥 " + profile.KeyPath
						if profile.HasPass {
							sub += "（加密）"
						}
					default:
						sub = "密码登录"
					}
					ui.Text(c, sub).FontSize(12).TextColor(t.TextMuted)
				})
				if ui.Button(c, "连接").Clicked() {
					l.cardErr = ""
					l.editForm(profile, true)
				}
				if ui.Button(c, "删除").Clicked() {
					l.Delete(profile)
				}
			})
		}
	})
}

// editForm opens the form: filled from p when it connects a saved host
// (fresh says so), or empty for a new one.
func (l *List) editForm(p profiles.Profile, fresh bool) {
	f := &l.form
	f.open = true
	f.editing = p.ID
	f.label = p.Label
	f.host = p.Host
	f.port = p.Port
	f.user = p.User
	if !fresh {
		f.label, f.host, f.port, f.user = "", "", "", ""
	}
	switch {
	case p.Auth == profiles.Key:
		f.auth = 1
		f.keyPath = p.KeyPath
	default:
		f.auth = 0
		f.keyPath = ""
	}
	f.password, f.passphr = "", ""
}

// formView is the new connection form.
func (l *List) formView(c *ui.Context) {
	f := &l.form
	t := c.Theme()
	ui.Column(c).Gap(14).Width(420).Children(func() {
		ui.Text(c, "新建连接").FontSize(18).Bold()

		ui.Field(c, "名称（可选）", func() {
			ui.TextInput(c, &f.label).Placeholder("如 公司服务器")
		})
		ui.Field(c, "主机", func() {
			ui.TextInput(c, &f.host).Placeholder("example.com")
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Column(c).Gap(6).Width(110).Children(func() {
				ui.Field(c, "端口", func() {
					ui.TextInput(c, &f.port).Placeholder("22")
				})
			})
			ui.Column(c).Gap(6).Grow(1).Children(func() {
				ui.Field(c, "用户名", func() {
					ui.TextInput(c, &f.user).Placeholder("root")
				})
			})
		})

		ui.Field(c, "认证方式", func() {
			ui.Segmented(c, &f.auth, "密码", "私钥")
		})
		if f.auth == 0 { // 密码
			ui.Field(c, "登录密码", func() {
				ui.TextInput(c, &f.password).Password().Placeholder("登录密码")
			})
		} else {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "选择私钥文件…").Clicked() {
					l.pickKey()
				}
				if f.keyPath != "" {
					ui.Text(c, f.keyPath).FontSize(12).TextColor(t.TextMuted).Grow(1)
					if ui.Button(c, "清除").Clicked() {
						f.keyPath = ""
					}
				} else {
					ui.Text(c, "未选择").FontSize(12).TextColor(c.Theme().TextMuted).Grow(1)
				}
			})
			ui.Field(c, "私钥口令（可空）", func() {
				ui.TextInput(c, &f.passphr).Password().Placeholder("加密私钥的口令")
			})
		}

		ui.Row(c).Gap(12).Children(func() {
			if ui.Button(c, "返回").Clicked() {
				l.form.open = false
			}
			if ui.PrimaryButton(c, "连接").Clicked() {
				authType := profiles.Password
				keyPath := ""
				if f.auth == 1 {
					authType = profiles.Key
					keyPath = f.keyPath
				}
				p := profiles.Profile{
					ID:      f.editing, // 连接已存主机时保持它,不挤出新条目
					Label:   f.label,
					Host:    f.host,
					Port:    f.port,
					User:    f.user,
					Auth:    authType,
					KeyPath: keyPath,
					HasPass: true, // 密码与口令只留在本页,不入盘
				}
				if _, err := l.store.Save(p); err != nil {
					// 表单不完整:不动, ErrorMessage 说缺了什么。
					l.err = ErrorText(err)
					return
				}
				l.err = ""
				l.form.open = false
				secret := Secret{Password: f.password, KeyPath: keyPath, Passphrase: f.passphr}
				l.Connect(p, secret)
			}
		})
		if l.err != "" {
			ui.Text(c, l.err).FontSize(12).TextColor(c.Theme().Danger)
		}
	})
}

// pickKey opens the private key file dialog off the view's goroutine and
// fills the form with the choice.
func (l *List) pickKey() {
	if l.form.choosing {
		return
	}
	l.form.choosing = true
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: l.Win,
			Title:  "选择私钥",
		})
		mygo.RunOnMain(func() {
			l.form.choosing = false
			if err == nil && len(paths) > 0 {
				l.form.keyPath = paths[0]
			}
			if l.Win != nil {
				l.Win.Update(func() {})
			}
		})
	}()
}

// ErrorText returns a human-readable message for a connect failure, for
// the pages to show.
func ErrorText(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("连接失败:%v", err)
}
