package appui

import (
	"lumeshell/internal/sshclient"

	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/display"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/theme"
)

// TunnelView manages the connection's port forwards: it lists what is
// listening and takes a new one's spec. The conn it works on is the
// app's live one, so forwards stay up as long as the session does.
type TunnelView struct {
	conn *sshclient.Client

	open    bool
	kind    int // 0 local, 1 remote, 2 dynamic
	bind    string
	target  string
	err     string
	errGone bool
}

// NewTunnelView makes the manager over the connection.
func NewTunnelView(conn *sshclient.Client) *TunnelView {
	return &TunnelView{conn: conn}
}

// Open shows the dialog.
func (t *TunnelView) Open() { t.open = true }

// View builds the dialog while it is open; nothing renders otherwise.
func (t *TunnelView) View(c *ui.Context) {
	if !t.open || t.conn == nil {
		return
	}
	kinds := []string{"本地 (-L)", "远端 (-R)", "动态 (-D)"}
	ok := feedback.Modal(c, &t.open, feedback.ModalProps{
		Title:      "端口转发",
		Width:      460,
		OkText:     "关闭",
		NoFooter:   true, // 隧道即开即生效,没有"确定"这一说;关闭只是收起
		CancelText: "关闭",
	}, func() {
		ui.Column(c).Gap(14).Children(func() {
			if t.err != "" {
				feedback.Alert(c, t.err, feedback.AlertProps{Type: feedback.Error, Closed: &t.errGone})
			}
			// 已开的隧道,带关闭按钮。
			live := t.conn.Tunnels()
			if len(live) > 0 {
				ui.Column(c).Gap(4).Children(func() {
					for _, tn := range live {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							display.Tag(c, tn.Kind, display.TagProps{Size: theme.Small})
							ui.Text(c, tn.Bind).FontSize(13).Grow(1).SingleLine()
							if tn.Target != "" {
								ui.Text(c, "→ "+tn.Target).FontSize(12).TextColor(theme.TokensOf(c).TextSecondary).SingleLine()
							}
							if action.ActionIcon(c, lucide.X(), action.ActionIconProps{Title: "关闭"}).Clicked() {
								t.conn.CloseTunnel(tn)
							}
						})
					}
				})
				ui.Divider(c)
			}
			// 新建一条。
			action.Segmented(c, &t.kind, kinds, action.SegmentedProps{Block: true})
			ui.Field(c, bindLabel(t.kind), func() {
				field.Input(c, &t.bind, field.InputProps{Placeholder: bindPlaceholder(t.kind)})
			})
			if t.kind != 2 { // dynamic 没有固定目标
				ui.Field(c, targetLabel(t.kind), func() {
					field.Input(c, &t.target, field.InputProps{Placeholder: targetPlaceholder(t.kind)})
				})
			}
			if action.Button(c, "开启", action.ButtonProps{Variant: action.Primary}).Clicked() {
				t.start()
			}
		})
	})
	_ = ok
}

func bindLabel(kind int) string {
	if kind == 1 {
		return "服务器监听地址"
	}
	return "本地监听地址"
}

func bindPlaceholder(kind int) string {
	switch kind {
	case 1:
		return "0.0.0.0:9000"
	case 2:
		return "127.0.0.1:1080"
	}
	return "127.0.0.1:8080"
}

func targetLabel(kind int) string {
	if kind == 1 {
		return "本机目标地址"
	}
	return "服务器侧目标地址"
}

func targetPlaceholder(kind int) string {
	if kind == 1 {
		return "127.0.0.1:3000"
	}
	return "db.internal:5432"
}

// start opens the forward the form describes.
func (t *TunnelView) start() {
	var err error
	var tn *sshclient.Tunnel
	switch t.kind {
	case 1:
		tn, err = t.conn.ForwardRemote(t.bind, t.target)
	case 2:
		tn, err = t.conn.ForwardDynamic(t.bind)
	default:
		tn, err = t.conn.ForwardLocal(t.bind, t.target)
	}
	if err != nil {
		t.err, t.errGone = err.Error(), false
		return
	}
	t.err = ""
	t.bind, t.target = "", ""
	_ = tn // 列表已加进 conn.Tunnels,下一帧自然出现
}
