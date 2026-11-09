// Package appui builds the app's pages: the list of saved hosts and the
// form a new connection starts from. It holds no connections; main.go
// makes them and tells this package what happened.
package appui

import (
	"fmt"

	"lume/internal/profiles"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/theme"
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
		jumpHost string // 可空:配了就先连它再到目标
		jumpPort string
		jumpUser string
		choosing bool   // a file dialog is up
		editing  string // the profile being connected, empty for a new one
	}
	err         string // the form's error, from its last connect attempt
	cardErr     string // what the last connect attempt failed with, for the cards
	cardErrGone bool   // the error banner's close button sets it, FailedCard clears it
	formErrGone bool   // the form banner's close sets it; a failed connect clears it
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
func (l *List) FailedCard(why string) { l.cardErr, l.cardErrGone = why, false }

// View builds the page.
func (l *List) View(c *ui.Context) {
	tk := theme.TokensOf(c)
	ui.Column(c).Fill().Padding(32).Gap(24).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Lume").FontSize(28).Bold()
			ui.Text(c, "SSH 终端").FontSize(14).TextColor(tk.TextSecondary)
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
	ps := l.store.List()
	if l.cardErr != "" {
		ui.Row(c).Width(560).Children(func() {
			feedback.Alert(c, l.cardErr, feedback.AlertProps{
				Type:        feedback.Error,
				Description: "检查主机与认证后重试",
				Closed:      &l.cardErrGone,
			})
		})
	}
	if len(ps) == 0 {
		display.Empty(c, display.EmptyProps{
			Icon:        lucide.Server(),
			Title:       "暂无保存的主机",
			Description: "新建一个连接,登录信息只存在内存里",
			Action: func() {
				if action.Button(c, "新建连接", action.ButtonProps{Variant: action.Primary}).Clicked() {
					l.editForm(profiles.Profile{}, false)
				}
			},
		})
		return
	}
	ui.Row(c).Width(560).Justify(ui.End).Children(func() {
		if action.Button(c, "新建连接", action.ButtonProps{Variant: action.Primary}).Clicked() {
			l.editForm(profiles.Profile{}, false)
		}
	})
	layout.ScrollArea(c, layout.ScrollAreaProps{Fade: true}, func() {
		ui.Column(c).Gap(10).FillWidth().Width(560).Padding(2).Children(func() {
			for _, p := range ps {
				l.cardView(c, p)
			}
		})
	}).Grow(1)
}

// cardView is one saved host: its name and endpoint beside the way it
// logs in, with 连接 filling the form and 删除 dropping it.
func (l *List) cardView(c *ui.Context, profile profiles.Profile) {
	layout.Block(c, layout.BlockProps{Variant: layout.BlockOutlined, Shadow: true}).
		FillWidth().Padding(14).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Icon(c, lucide.Server()).FontSize(20).TextColor(theme.TokensOf(c).TextTertiary)
			ui.Column(c).Grow(1).Gap(4).MinWidth(0).Children(func() {
				ui.Text(c, profile.Title()).Bold()
				ui.Text(c, profile.Endpoint()).FontSize(12).TextColor(theme.TokensOf(c).TextSecondary)
				ui.Row(c).Gap(6).Children(func() {
					if profile.Auth == profiles.Key {
						display.Tag(c, "私钥", display.TagProps{Color: display.TagGeekblue, Size: theme.Small})
						sub := profile.KeyPath
						if profile.HasPass {
							sub += "（加密）"
						}
						ui.Text(c, sub).FontSize(11).TextColor(theme.TokensOf(c).TextTertiary).SingleLine()
					} else {
						display.Tag(c, "密码", display.TagProps{Size: theme.Small})
					}
				})
			})
			if action.Button(c, "连接", action.ButtonProps{Variant: action.Default}).Clicked() {
				l.cardErr, l.cardErrGone = "", false
				l.editForm(profile, true)
			}
			if action.ActionIcon(c, lucide.Trash2(), action.ActionIconProps{
				Danger: true, Title: "删除",
			}).Clicked() {
				l.Delete(profile)
			}
		})
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
	f.jumpHost, f.jumpPort, f.jumpUser = p.JumpHost, p.JumpPort, p.JumpUser
	if !fresh {
		f.label, f.host, f.port, f.user = "", "", "", ""
		f.jumpHost, f.jumpPort, f.jumpUser = "", "", ""
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

// formView is the connection form in a dialog: filled from a card to
// connect it, empty for a new host. Modal's OK runs the connect and
// leaves *open alone; the connect itself closes it on success.
func (l *List) formView(c *ui.Context) {
	f := &l.form
	title := "新建连接"
	if f.editing != "" {
		title = "连接 " + f.host
	}
	ok := feedback.Modal(c, &f.open, feedback.ModalProps{
		Title:      title,
		Width:      460,
		OkText:     "连接",
		CancelText: "取消",
	}, func() {
		ui.Column(c).Gap(14).Children(func() {
			if l.err != "" {
				feedback.Alert(c, l.err, feedback.AlertProps{
					Type:   feedback.Error,
					Closed: &l.formErrGone,
				})
			}
			ui.Field(c, "名称（可选）", func() {
				field.Input(c, &f.label, field.InputProps{Placeholder: "如 公司服务器"})
			})
			ui.Field(c, "主机", func() {
				field.Input(c, &f.host, field.InputProps{Placeholder: "example.com"})
			})
			ui.Row(c).Gap(12).Children(func() {
				ui.Column(c).Gap(6).Width(110).Children(func() {
					ui.Field(c, "端口", func() {
						field.Input(c, &f.port, field.InputProps{Placeholder: "22"})
					})
				})
				ui.Column(c).Gap(6).Grow(1).Children(func() {
					ui.Field(c, "用户名", func() {
						field.Input(c, &f.user, field.InputProps{Placeholder: "root"})
					})
				})
			})
			ui.Field(c, "认证方式", func() {
				action.Segmented(c, &f.auth, []string{"密码", "私钥"}, action.SegmentedProps{Block: true})
			})
			// 跳板机可空:填了就先连它再到目标,认证沿用上面的方式。
			ui.Field(c, "跳板机（可空）", func() {
				ui.Row(c).Gap(8).Children(func() {
					ui.Column(c).Grow(2).Children(func() {
						field.Input(c, &f.jumpHost, field.InputProps{Placeholder: "jump.example.com"})
					})
					ui.Column(c).Width(70).Children(func() {
						field.Input(c, &f.jumpPort, field.InputProps{Placeholder: "22"})
					})
					ui.Column(c).Grow(1).Children(func() {
						field.Input(c, &f.jumpUser, field.InputProps{Placeholder: "同用户名"})
					})
				})
			})
			if f.auth == 0 { // 密码
				ui.Field(c, "登录密码", func() {
					field.InputPassword(c, &f.password, field.InputProps{Placeholder: "登录密码"})
				})
			} else {
				ui.Field(c, "私钥文件", func() {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						if action.Button(c, "选择私钥…", action.ButtonProps{}).Clicked() {
							l.pickKey()
						}
						if f.keyPath != "" {
							ui.Text(c, f.keyPath).FontSize(12).TextColor(theme.TokensOf(c).TextSecondary).Grow(1).SingleLine()
							if action.ActionIcon(c, lucide.X(), action.ActionIconProps{Title: "清除"}).Clicked() {
								f.keyPath = ""
							}
						} else {
							ui.Text(c, "未选择").FontSize(12).TextColor(theme.TokensOf(c).TextTertiary).Grow(1)
						}
					})
				})
				ui.Field(c, "私钥口令（可空）", func() {
					field.InputPassword(c, &f.passphr, field.InputProps{Placeholder: "加密私钥的口令"})
				})
			}
		})
	})
	if ok {
		l.submit()
	}
}

// submit runs the form's connect: it saves what the store keeps (no
// secret) and calls Connect with what it cannot.
func (l *List) submit() {
	f := &l.form
	authType := profiles.Password
	keyPath := ""
	if f.auth == 1 {
		authType = profiles.Key
		keyPath = f.keyPath
	}
	p := profiles.Profile{
		ID:       f.editing, // 连接已存主机时保持它,不挤出新条目
		Label:    f.label,
		Host:     f.host,
		Port:     f.port,
		User:     f.user,
		Auth:     authType,
		KeyPath:  keyPath,
		HasPass:  true, // 密码与口令只留在本页,不入盘
		JumpHost: f.jumpHost,
		JumpPort: f.jumpPort,
		JumpUser: f.jumpUser,
	}
	if _, err := l.store.Save(p); err != nil {
		// 表单不完整:对话框留着,Alert 说缺了什么。
		l.err, l.formErrGone = err.Error(), false
		return
	}
	l.err = ""
	l.form.open = false
	l.Connect(p, Secret{Password: f.password, KeyPath: keyPath, Passphrase: f.passphr})
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
