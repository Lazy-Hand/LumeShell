// Lume is an SSH client: the user picks a host, the app connects and
// shows its terminal, and file transfers ride the same connection. The
// window shows one page at a time: the host list, or a session's
// terminal and its files.
package main

import (
	"log"
	"sync"

	"lume/internal/appui"
	"lume/internal/filepanel"
	"lume/internal/profiles"
	"lume/internal/sshclient"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/theme"
)

// app is the state the window shows. Its view builds the interface from
// it, on the main thread, whenever the window needs a frame: after
// input, after Window.Update, and while something animates. The
// connection's parts, and the time a session's end arrives, belong to
// the goroutines; everything they change the view shows goes through
// win.Update.
type app struct {
	store *profiles.Store
	list  *appui.List
	hub   appui.Hub
	win   *mygo.Window

	// connMu guards the session's parts on the screen.
	connMu sync.Mutex
	conn   *sshclient.Client
	term   *terminal.Terminal
	panel  *filepanel.Panel

	dial dialGuard
}

// dialGuard takes one connect attempt at a time: a second click while a
// dial is up does nothing, rather than piling a second session's
// dialogs and timeouts on the first's.
type dialGuard struct {
	mu sync.Mutex
	up bool
}

func (g *dialGuard) try() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.up {
		return false
	}
	g.up = true
	return true
}

func (g *dialGuard) done() {
	g.mu.Lock()
	g.up = false
	g.mu.Unlock()
}

func main() {
	a := &app{}
	var listErr error
	mygo.App.WhenReady(func() {
		dir, err := mygo.App.Path(mygo.PathUserData)
		if err == nil {
			a.store, listErr = profiles.Open(dir)
		}
		a.list = appui.NewList(a.store)
		a.list.Connect = a.connect
		a.list.Delete = func(p profiles.Profile) { _ = a.store.Remove(p.ID) }
		a.hub.List = a.list
		a.hub.Now = appui.Home
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "Lume",
			Width:     960,
			Height:    640,
			MinWidth:  720,
			MinHeight: 480,
			StateKey:  "main",
			// 保存的主机打不开时列表仍然进来,错误随卡片可见。
			Content: ui.View(a.view),
		})
		a.hub.Win = a.win
		a.win.OnClosed(func() { a.teardown() })
		if listErr != nil {
			a.win.Update(func() {})
			mygo.Dialog.Error("配置", "读取已存主机失败:"+listErr.Error())
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// view builds the page the hub shows, in the Lobe UI theme.
func (a *app) view(c *ui.Context) {
	theme.Apply(c)
	a.hub.View(c)
}

// connect starts a session with the profile and secret the list page
// gave. The dial runs off the UI, and the terminal comes up when the
// connection is there. 文件浏览是另一页、另一条通道:它连不上不影响
// 终端,从终端页的工具条进。
func (a *app) connect(p profiles.Profile, s appui.Secret) {
	if !a.dial.try() {
		return // 一次只有一次拨号;重复的点击忽略。
	}
	go func() {
		defer a.dial.done()
		auth, err := authOf(p, s)
		var conn *sshclient.Client
		if err == nil {
			conf := sshclient.Config{Host: p.Host, Port: p.Port, User: p.User, Auth: auth}
			if p.JumpHost != "" {
				conf.Jump = &sshclient.Jump{
					Host: p.JumpHost,
					Port: p.JumpPort,
					User: p.JumpUser,
					Auth: auth, // 跳板复用同一种认证,口令也是同一个
				}
			}
			conn, err = sshclient.Connect(conf)
		}
		if err != nil {
			a.failConnect(appui.ErrorText(err))
			return
		}
		// The store keeps where to connect, not the secret; the connect
		// counts as the profile's use.
		_ = a.store.Touch(p.ID)
		fail := func(why error) {
			conn.Close()
			a.failConnect(appui.ErrorText(why))
		}
		sess, err := conn.Shell(80, 24)
		if err != nil {
			fail(err)
			return
		}
		term, err := terminal.New(terminal.Options{
			Conn:        sess,
			OptionAsAlt: true,
			OnTitle:     func(title string) { a.win.SetTitle(title) },
			OnExit:      func(int) { a.sessionEnded(p) },
		})
		if err != nil {
			sess.Close()
			fail(err)
			return
		}
		a.connMu.Lock()
		a.conn, a.term = conn, term
		tv := appui.NewTunnelView(conn)
		page := &appui.TermPage{
			Profile: p,
			Term:    term,
			Back:    func() { a.leaveSession() },
			ToFiles: func() { a.filesPage(p) },
			Tunnels: func() { tv.Open() },
		}
		a.connMu.Unlock()
		a.win.Update(func() {
			a.hub.Term = page
			a.hub.Tunnels = tv
			a.hub.Now = appui.Session
		})
	}()
}

// filesPage 起文件浏览器:左列本机文件,右列服务器文件,各自独立
// 的目录与列表。SFTP 起不来时只报错,终端不会被它带走。
func (a *app) filesPage(p profiles.Profile) {
	a.connMu.Lock()
	conn := a.conn
	a.connMu.Unlock()
	if conn == nil {
		return
	}
	if a.hub.Files != nil && a.hub.Now == appui.Files {
		a.win.Update(func() { a.hub.Now = appui.Files })
		return
	}
	if a.hub.Files != nil {
		a.win.Update(func() { a.hub.Now = appui.Files })
		return
	}
	go func() {
		fs, err := conn.SFTP()
		if err != nil {
			a.win.Update(func() { a.list.FailedCard("文件服务不可用:" + appui.ErrorText(err)) })
			return
		}
		home, err := fs.Home()
		if err != nil {
			home = "/"
		}
		changed := func() { a.win.Update(func() {}) }
		remote := filepanel.New(fs, home)
		localDir := filepanel.LocalHome()
		local := filepanel.New(filepanel.LocalFS{}, localDir)
		remote.SetOther(local, changed)
		local.SetOther(remote, changed)
		remote.Open(home, changed)
		local.Open(localDir, changed)
		a.connMu.Lock()
		if a.panel != nil && a.panel != remote {
			a.panel.Close() // 上次会话留下的 SFTP 通道
		}
		a.panel = remote
		page := &appui.FilesPage{
			Profile:   p,
			Remote:    remote,
			Local:     local,
			SideWidth: 420,
			Back:      func() { a.toTerminal() },
		}
		a.connMu.Unlock()
		a.win.Update(func() {
			a.hub.Files = page
			a.hub.Now = appui.Files
		})
	}()
}

// toTerminal returns from the file browser to the terminal of the same
// session.
func (a *app) toTerminal() {
	a.win.Update(func() { a.hub.Now = appui.Session })
}

// failConnect reports why a session did not come up, beside the cards
// of the list. A modal dialog here would hang over a session that
// connects later, so the failure stays in the page.
func (a *app) failConnect(why string) {
	a.win.Update(func() {
		a.list.FailedCard(why)
		a.list.ShowHome()
		a.hub.Now = appui.Home
	})
}

// leaveSession tears the session down and goes back to the list, as the
// user asked.
func (a *app) leaveSession() {
	a.teardown()
	a.hub.Term = nil
	a.hub.Tunnels = nil
	a.hub.Now = appui.Home
	a.win.Update(func() {})
}

// sessionEnded runs when the terminal reports its session's end, which
// includes the connection's: it goes back to the list and says so.
func (a *app) sessionEnded(p profiles.Profile) {
	a.connMu.Lock()
	still := a.term != nil && a.hub.Now == appui.Session
	a.connMu.Unlock()
	if !still {
		return
	}
	a.teardown()
	a.hub.Term = nil
	a.win.Update(func() {
		a.win.SetTitle("Lume")
		a.hub.Now = appui.Home
	})
	mygo.Dialog.Message(mygo.MessageOptions{
		Title:   "连接已断开",
		Message: p.Title() + " 的会话已结束",
	})
}

// teardown frees the session's parts, keeping the page as it is until
// the caller moves it.
func (a *app) teardown() {
	a.connMu.Lock()
	conn, term, panel := a.conn, a.term, a.panel
	a.conn, a.term, a.panel = nil, nil, nil
	a.connMu.Unlock()
	if term != nil {
		term.Close()
	}
	if panel != nil {
		panel.Close()
	}
	if conn != nil {
		conn.Close()
	}
}

// authOf builds the connection's auth from the profile and what the
// user just typed. A saved profile's card carries no secret, so it
// connects only when the host takes a key with no passphrase; a failed
// auth sends the user back with the server's message.
func authOf(p profiles.Profile, s appui.Secret) (sshclient.Auth, error) {
	switch p.Auth {
	case profiles.Key:
		return sshclient.KeyAuth{KeyPath: p.KeyPath, Passphrase: s.Passphrase}, nil
	default:
		return sshclient.PasswordAuth{Password: s.Password}, nil
	}
}
