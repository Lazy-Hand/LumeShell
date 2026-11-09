package appui

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/theme"
	"lume/internal/filepanel"
	"lume/internal/profiles"
)

// Page is what the window shows: the list of hosts, the terminal of the
// connection made, or the two-column file browser over the same
// connection.
type Page int

// The pages.
const (
	// Home is the list of saved hosts.
	Home Page = iota
	// Session is one connected host's terminal.
	Session
	// Files is the two-column file browser: local left, remote right.
	Files
)

// TermPage is the terminal of one connected host. The file browser is
// its own page: the terminal works without SFTP, and SFTP fails without
// taking the session with it. Snippets rides the page too, a drawer of
// saved commands a click sends to the terminal.
type TermPage struct {
	Profile  profiles.Profile
	Term     *terminal.Terminal
	Snippets *SnippetView

	// Back runs when the user leaves the terminal; ToFiles opens the
	// file browser over the same connection; Tunnels opens the port
	// forward manager.
	Back    func()
	ToFiles func()
	Tunnels func()
}

// View builds the page.
func (p *TermPage) View(c *ui.Context) {
	tk := theme.TokensOf(c)
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Gap(10).Padding(8, 14).AlignItems(ui.Center).Background(tk.BgContainer).Children(func() {
			if action.Button(c, "断开", action.ButtonProps{Size: theme.Small}).Clicked() && p.Back != nil {
				p.Back()
			}
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, p.Profile.Title()).Bold()
				ui.Text(c, p.Profile.Endpoint()).FontSize(11).TextColor(tk.TextTertiary)
			})
			if action.ActionIcon(c, lucide.TerminalSquare(), action.ActionIconProps{
				Title: "命令片段",
			}).Clicked() && p.Snippets != nil {
				p.Snippets.Open()
			}
			if action.ActionIcon(c, lucide.Globe(), action.ActionIconProps{
				Title: "隧道",
			}).Clicked() && p.Tunnels != nil {
				p.Tunnels()
			}
			if action.ActionIcon(c, lucide.FolderOpen(), action.ActionIconProps{
				Title: "文件",
			}).Clicked() && p.ToFiles != nil {
				p.ToFiles()
			}
		})
		ui.Divider(c)
		terminal.View(c, p.Term).Fill().AutoFocus()
	})
	if p.Snippets != nil {
		p.Snippets.View(c, p.Term)
	}
}

// FilesPage is the two-column file browser over one connection: the
// local machine's files on the left, the server's on the right. Each
// side browses on its own; whose 出发 the transfer the middle tells.
type FilesPage struct {
	Profile   profiles.Profile
	Remote    *filepanel.Panel // the server's files
	Local     *filepanel.Panel // this machine's files
	SideWidth float32          // the local column's width
	Closed    bool             // the middle's 收起, one column each side

	// Back runs when the user leaves the browser; Upload and Download
	// run with a local path and the remote panel's directory.
	Back func()
}

// View builds the page.
func (p *FilesPage) View(c *ui.Context) {
	tk := theme.TokensOf(c)
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Gap(10).Padding(8, 14).AlignItems(ui.Center).Background(tk.BgContainer).Children(func() {
			if action.ActionIcon(c, lucide.ChevronLeft(), action.ActionIconProps{
				Title: "返回终端",
			}).Clicked() && p.Back != nil {
				p.Back()
			}
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, p.Profile.Title()+" 的文件").Bold()
				ui.Text(c, p.Profile.Endpoint()).FontSize(11).TextColor(tk.TextTertiary)
			})
		})
		ui.Divider(c)
		ui.Split(c, &p.SideWidth, func() {
			filepanel.View(c, p.Local, nil)
		}, func() {
			filepanel.View(c, p.Remote, nil)
		}).Fill().Grow(1)
	})
}

// Hub is the app's pages over one window.
type Hub struct {
	List    *List
	Term    *TermPage
	Files   *FilesPage
	Tunnels *TunnelView // the session's port forwards, when one is up
	Now     Page
	Win     *mygo.Window
	Store   *profiles.Store
}

// View builds the page the hub shows.
func (h *Hub) View(c *ui.Context) {
	if h.Tunnels != nil {
		h.Tunnels.View(c) // 对话框浮在任意页上
	}
	switch h.Now {
	case Session:
		if h.Term != nil {
			h.Term.View(c)
		}
	case Files:
		if h.Files != nil {
			h.Files.View(c)
		}
	default:
		if h.List != nil {
			h.List.Win = h.Win
			h.List.View(c)
		}
	}
}
