package appui

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"lume/internal/filepanel"
	"lume/internal/profiles"
)

// Page is what the window shows: the list of hosts, or the terminal of
// the connection made.
type Page int

// The pages.
const (
	// Home is the list of saved hosts.
	Home Page = iota
	// Session is one connected host: its terminal and its files.
	Session
)

// TermPage is the session's page: the terminal on the left and the
// host's files on the right, split by a divider the user drags.
type TermPage struct {
	Profile profiles.Profile
	Term    *terminal.Terminal
	Files   *filepanel.Panel
	// SplitWidth is the file panel's width, what the divider keeps.
	SplitWidth float32
	// Open says the file panel shows; the toolbar's button turns it off
	// and on. It starts as it is set.
	Open bool

	// Back runs when the user leaves the session.
	Back func()
}

// View builds the page.
func (p *TermPage) View(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Gap(10).Padding(10, 14).AlignItems(ui.Center).Background(t.Surface).Children(func() {
			if ui.Button(c, "断开").Clicked() && p.Back != nil {
				p.Back()
			}
			ui.Column(c).Gap(2).Grow(1).Children(func() {
				ui.Text(c, p.Profile.Title()).Bold()
				ui.Text(c, p.Profile.Endpoint()).FontSize(11).TextColor(t.TextMuted)
			})
			if ui.Button(c, "文件").Clicked() {
				p.Open = !p.Open
			}
		})
		if p.Open && p.Files != nil {
			ui.Split(c, &p.SplitWidth, func() {
				terminal.View(c, p.Term).Fill().AutoFocus()
			}, func() {
				filepanel.View(c, p.Files, nil)
			}).Fill().Grow(1)
		} else {
			terminal.View(c, p.Term).Fill().AutoFocus()
		}
	})
}

// Hub is the app's pages over one window.
type Hub struct {
	List  *List
	Term  *TermPage
	Now   Page
	Win   *mygo.Window
	Store *profiles.Store
}

// View builds the page the hub shows.
func (h *Hub) View(c *ui.Context) {
	switch h.Now {
	case Session:
		if h.Term != nil {
			h.Term.View(c)
		}
	default:
		if h.List != nil {
			h.List.Win = h.Win
			h.List.View(c)
		}
	}
}
