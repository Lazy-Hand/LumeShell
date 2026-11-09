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

// view builds the page the hub shows.
func (a *app) view(c *ui.Context) { a.hub.View(c) }

// connect starts a session with the profile and secret the list page
// gave. The dial runs off the UI, and the terminal comes up when the
// connection is there. The file panel rides the same connection; when
// the host has no SFTP (rare), the terminal alone comes up.
func (a *app) connect(p profiles.Profile, s appui.Secret) {
	go func() {
		auth, err := authOf(p, s)
		var conn *sshclient.Client
		if err == nil {
			conf := sshclient.Config{Host: p.Host, Port: p.Port, User: p.User, Auth: auth}
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
		changed := func() { a.win.Update(func() {}) }
		var panel *filepanel.Panel
		if fs, err := conn.SFTP(); err == nil {
			home, err := fs.Home()
			if err != nil {
				home = "/"
			}
			panel = filepanel.New(fs, home)
			panel.Open(home, changed)
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
		a.conn, a.term, a.panel = conn, term, panel
		page := &appui.TermPage{
			Profile:    p,
			Term:       term,
			Files:      panel,
			Open:       panel != nil,
			SplitWidth: 280,
			Back:       func() { a.leaveSession() },
		}
		a.connMu.Unlock()
		a.win.Update(func() {
			a.hub.Term = page
			a.hub.Now = appui.Session
		})
	}()
}

// failConnect reports why a session did not come up, and returns to the
// list.
func (a *app) failConnect(why string) {
	a.win.Update(func() {
		a.list.ShowHome()
		a.hub.Now = appui.Home
	})
	mygo.Dialog.Error("连接失败", why)
}

// leaveSession tears the session down and goes back to the list, as the
// user asked.
func (a *app) leaveSession() {
	a.teardown()
	a.hub.Term = nil
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
