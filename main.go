// Lume is an SSH client: the user picks a host, the app connects and
// shows its terminal, and file transfers ride the same connection. The
// window shows one page at a time: the host list, or a session's
// terminal and its files. Several sessions can be open at once, as
// tabs along the top.
package main

import (
	"fmt"
	"io"
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

// session is one live connection: its SSH client, terminal, file
// panels, port forwards, and the pages they draw on.
type session struct {
	id      string // profile ID + seq, the tab's key
	profile profiles.Profile
	secret  appui.Secret // 内存里,断开自动重连用;不入盘
	ending  bool         // 用户主动断开时置真,sessionEnded 不再重连
	conn    *sshclient.Client
	term    *terminal.Terminal
	termPg  *appui.TermPage
	filesPg *appui.FilesPage
	tunnelV *appui.TunnelView
	trigV   *appui.TriggerView
	watcher *appui.TriggerWatcher
	panel   *filepanel.Panel // the remote panel, for closing
}

// scanConn wraps a session's Conn so what the terminal reads also
// passes the trigger watcher. Only Read is touched; the rest delegates.
type scanConn struct {
	io.ReadWriteCloser
	w *appui.TriggerWatcher
}

func (s scanConn) Read(p []byte) (int, error) {
	n, err := s.ReadWriteCloser.Read(p)
	if n > 0 {
		s.w.Scan(p[:n])
	}
	return n, err
}

// Resize passes through to the wrapped Conn so the remote still hears
// the view's size.
func (s scanConn) Resize(cols, rows int) error {
	if r, ok := s.ReadWriteCloser.(interface {
		Resize(cols, rows int) error
	}); ok {
		return r.Resize(cols, rows)
	}
	return nil
}

// app is the state the window shows. Its view builds the interface from
// it, on the main thread, whenever the window needs a frame: after
// input, after Window.Update, and while something animates.
type app struct {
	store *profiles.Store
	list  *appui.List
	hub   appui.Hub
	win   *mygo.Window

	// connMu guards the sessions and which one the hub shows.
	connMu sync.Mutex
	seq    int
	active string
	sess   map[string]*session

	dial dialGuard
}

// dialGuard takes one connect attempt at a time: a second click while a
// dial is up does nothing.
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
	a := &app{sess: map[string]*session{}}
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
			Content:   ui.View(a.view),
		})
		a.hub.Win = a.win
		a.win.OnClosed(func() { a.teardownAll() })
		if listErr != nil {
			a.win.Update(func() {})
			mygo.Dialog.Error("配置", "读取已存主机失败:"+listErr.Error())
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// view builds the tab bar over the page the hub shows.
func (a *app) view(c *ui.Context) {
	theme.Apply(c)
	a.tabs(c)
	a.hub.View(c)
}

// tabs draws a tab per open session: clicking one brings its page up,
// the ✕ closes it. Nothing renders with fewer than two sessions.
func (a *app) tabs(c *ui.Context) {
	a.connMu.Lock()
	sessions := make([]*session, 0, len(a.sess))
	for _, s := range a.sess {
		sessions = append(sessions, s)
	}
	active := a.active
	a.connMu.Unlock()
	if len(sessions) < 2 {
		return
	}
	tk := theme.TokensOf(c)
	ui.Row(c).Gap(2).Padding(4, 8).Background(tk.BgContainer).Children(func() {
		for _, s := range sessions {
			sess := s
			on := sess.id == active
			btn := ui.Row(c).Gap(6).Padding(4, 10).Radius(tk.BorderRadiusSM).Cursor(ui.CursorPointer).
				FontSize(12).TextColor(tk.TextSecondary)
			if on {
				btn.Background(tk.FillTertiary).TextColor(tk.Text)
			} else if btn.Hovered() {
				btn.Background(tk.FillQuaternary)
			}
			btn.Children(func() {
				ui.Text(c, sess.profile.Title()).SingleLine()
				if sess.watcher != nil && sess.watcher.Seen() {
					ui.Box(c).Width(6).Height(6).Radius(3).Background(tk.Error)
				}
				if ui.Text(c, "×").FontSize(13).TextColor(tk.TextTertiary).Cursor(ui.CursorPointer).Clicked() {
					a.closeSession(sess.id)
				}
			})
			if btn.Clicked() {
				a.showSession(sess.id)
			}
		}
	})
}

// connect starts a session with the profile and secret the list page
// gave. The dial runs off the UI; the terminal comes up when the
// connection is there.
func (a *app) connect(p profiles.Profile, s appui.Secret) {
	if !a.dial.try() {
		return
	}
	go func() {
		defer a.dial.done()
		auth, err := authOf(p, s)
		var conn *sshclient.Client
		if err == nil {
			conf := sshclient.Config{Host: p.Host, Port: p.Port, User: p.User, Auth: auth}
			if p.JumpHost != "" {
				conf.Jump = &sshclient.Jump{Host: p.JumpHost, Port: p.JumpPort, User: p.JumpUser, Auth: auth}
			}
			conn, err = sshclient.Connect(conf)
		}
		if err != nil {
			a.failConnect(appui.ErrorText(err))
			return
		}
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
		a.connMu.Lock()
		a.seq++
		id := fmt.Sprintf("%s-%d", p.ID, a.seq)
		a.connMu.Unlock()
		ss := &session{id: id, profile: p, secret: s, conn: conn}
		ss.watcher = appui.NewTriggerWatcher(func(matched string) { a.triggerHit(id, matched) })
		ss.trigV = appui.NewTriggerView(ss.watcher)
		var tconn io.ReadWriteCloser = sess
		if ss.watcher.HasPatterns() {
			tconn = scanConn{ReadWriteCloser: sess, w: ss.watcher}
		}
		term, err := terminal.New(terminal.Options{
			Conn:        tconn,
			OptionAsAlt: true,
			OnTitle:     func(title string) { a.win.SetTitle(title) },
			OnExit:      func(int) { a.sessionEnded(id) },
		})
		if err != nil {
			sess.Close()
			fail(err)
			return
		}
		ss.term = term
		ss.tunnelV = appui.NewTunnelView(conn)
		ss.termPg = &appui.TermPage{
			Profile:  p,
			Term:     term,
			Snippets: appui.NewSnippetView(),
			Triggers: ss.trigV,
			Back:     func() { a.leaveSession(id) },
			ToFiles:  func() { a.filesPage(id) },
			Tunnels:  func() { ss.tunnelV.Open() },
		}
		a.connMu.Lock()
		a.sess[id] = ss
		a.active = id
		a.connMu.Unlock()
		a.win.Update(func() {
			a.hub.Term = ss.termPg
			a.hub.Tunnels = ss.tunnelV
			a.hub.Now = appui.Session
		})
	}()
}

// filesPage opens the file browser of session id, building it the first
// time over that connection.
func (a *app) filesPage(id string) {
	a.connMu.Lock()
	ss, ok := a.sess[id]
	a.connMu.Unlock()
	if !ok {
		return
	}
	if ss.filesPg != nil {
		a.win.Update(func() {
			a.hub.Files = ss.filesPg
			a.hub.Now = appui.Files
		})
		return
	}
	go func() {
		fs, err := ss.conn.SFTP()
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
		remote.IsRemote = true
		localDir := filepanel.LocalHome()
		local := filepanel.New(filepanel.LocalFS{}, localDir)
		remote.SetOther(local, changed)
		local.SetOther(remote, changed)
		remote.Open(home, changed)
		local.Open(localDir, changed)
		page := &appui.FilesPage{
			Profile:   ss.profile,
			Remote:    remote,
			Local:     local,
			SideWidth: 420,
			Back:      func() { a.showSession(id) },
		}
		a.connMu.Lock()
		if cur, ok := a.sess[id]; ok {
			cur.filesPg = page
			cur.panel = remote
		}
		a.connMu.Unlock()
		a.win.Update(func() {
			a.hub.Files = page
			a.hub.Now = appui.Files
		})
	}()
}

// showSession brings session id's page up, terminal unless its files
// were the last thing it showed.
func (a *app) showSession(id string) {
	a.connMu.Lock()
	ss, ok := a.sess[id]
	if ok {
		a.active = id
	}
	a.connMu.Unlock()
	if !ok {
		return
	}
	if ss.watcher != nil {
		ss.watcher.Clear() // 看过了,红点消掉
	}
	a.win.Update(func() {
		if ss.filesPg != nil && a.hub.Now == appui.Files {
			a.hub.Files = ss.filesPg
		} else {
			a.hub.Term = ss.termPg
			a.hub.Now = appui.Session
		}
	})
}

// failConnect reports why a session did not come up.
func (a *app) failConnect(why string) {
	a.win.Update(func() {
		a.list.FailedCard(why)
		a.list.ShowHome()
		a.hub.Now = appui.Home
	})
}

// leaveSession closes the tab's session at the user's hand: it marks
// the session ending first, so sessionEnded won't try to reconnect.
func (a *app) leaveSession(id string) {
	a.connMu.Lock()
	if ss, ok := a.sess[id]; ok {
		ss.ending = true
	}
	a.connMu.Unlock()
	a.closeSession(id)
}

// sessionEnded runs when the terminal reports the connection's end:
// it tries to bring the session back once, and only drops the tab if
// the reconnect fails.
func (a *app) sessionEnded(id string) {
	a.connMu.Lock()
	ss, ok := a.sess[id]
	wasActive := a.active == id
	ending := ok && ss.ending
	a.connMu.Unlock()
	if !ok || ending {
		return
	}
	// 断开的不是自己点的:先重连一次,成功就不打扰。
	go func() {
		if a.reconnect(ss) {
			return
		}
		a.win.Update(func() {
			mygo.Dialog.Message(mygo.MessageOptions{
				Title:   "连接已断开",
				Message: ss.profile.Title() + " 的会话已结束",
			})
		})
		a.closeSession(id)
		if wasActive {
			a.win.Update(func() { a.win.SetTitle("Lume") })
		}
	}()
}

// reconnect brings a dead session back: same profile, same secret, a
// fresh connection and terminal swapped into the same tab. It reports
// whether the session is live again.
func (a *app) reconnect(ss *session) bool {
	auth, err := authOf(ss.profile, ss.secret)
	if err != nil {
		return false
	}
	conf := sshclient.Config{Host: ss.profile.Host, Port: ss.profile.Port, User: ss.profile.User, Auth: auth}
	if ss.profile.JumpHost != "" {
		conf.Jump = &sshclient.Jump{
			Host: ss.profile.JumpHost, Port: ss.profile.JumpPort,
			User: ss.profile.JumpUser, Auth: auth,
		}
	}
	conn, err := sshclient.Connect(conf)
	if err != nil {
		return false
	}
	sess, err := conn.Shell(80, 24)
	if err != nil {
		conn.Close()
		return false
	}
	var tconn io.ReadWriteCloser = sess
	if ss.watcher.HasPatterns() {
		tconn = scanConn{ReadWriteCloser: sess, w: ss.watcher}
	}
	term, err := terminal.New(terminal.Options{
		Conn:        tconn,
		OptionAsAlt: true,
		OnTitle:     func(title string) { a.win.SetTitle(title) },
		OnExit:      func(int) { a.sessionEnded(ss.id) },
	})
	if err != nil {
		sess.Close()
		conn.Close()
		return false
	}
	ss.conn, ss.term = conn, term
	ss.tunnelV = appui.NewTunnelView(conn)
	ss.termPg = &appui.TermPage{
		Profile:  ss.profile,
		Term:     term,
		Snippets: appui.NewSnippetView(),
		Triggers: ss.trigV,
		Back:     func() { a.leaveSession(ss.id) },
		ToFiles:  func() { a.filesPage(ss.id) },
		Tunnels:  func() { ss.tunnelV.Open() },
	}
	ss.filesPg, ss.panel = nil, nil // 旧 SFTP 随旧连接走了
	a.connMu.Lock()
	a.sess[ss.id] = ss
	a.connMu.Unlock()
	a.win.Update(func() {
		a.hub.Term = ss.termPg
		a.hub.Tunnels = ss.tunnelV
		a.hub.Now = appui.Session
	})
	return true
}

// triggerHit runs when the watcher's pattern matches session id's
// output: it flashes the window and marks the tab, which showing the
// session clears.
func (a *app) triggerHit(id, matched string) {
	a.connMu.Lock()
	_, ok := a.sess[id]
	active := a.active == id
	a.connMu.Unlock()
	if !ok || active {
		return // 正看着的会话不用提醒
	}
	a.win.FlashFrame(true)
	a.win.Update(func() {
		a.win.FlashFrame(false)
	})
	// watcher.fired 已经在 Scan 里立了;让 UI 刷出红点靠 Update。
}

// closeSession frees one session's parts and shows what is left.
func (a *app) closeSession(id string) {
	a.connMu.Lock()
	ss, ok := a.sess[id]
	if ok {
		delete(a.sess, id)
		if a.active == id {
			a.active = ""
			for k := range a.sess {
				a.active = k
				break
			}
		}
	}
	next := a.active
	a.connMu.Unlock()
	if !ok {
		return
	}
	ss.term.Close()
	if ss.panel != nil {
		ss.panel.Close()
	}
	ss.conn.Close()
	a.win.Update(func() {
		if next == "" {
			a.hub.Now = appui.Home
		} else if s2, ok := a.sess[next]; ok {
			a.hub.Term = s2.termPg
			a.hub.Tunnels = s2.tunnelV
			a.hub.Now = appui.Session
		}
	})
}

// teardownAll ends every session.
func (a *app) teardownAll() {
	a.connMu.Lock()
	sessions := a.sess
	a.sess = map[string]*session{}
	a.active = ""
	a.connMu.Unlock()
	for _, ss := range sessions {
		ss.term.Close()
		if ss.panel != nil {
			ss.panel.Close()
		}
		ss.conn.Close()
	}
}

// authOf builds the connection's auth from the profile and what the
// user just typed.
func authOf(p profiles.Profile, s appui.Secret) (sshclient.Auth, error) {
	switch p.Auth {
	case profiles.Key:
		return sshclient.KeyAuth{KeyPath: p.KeyPath, Passphrase: s.Passphrase}, nil
	default:
		return sshclient.PasswordAuth{Password: s.Password}, nil
	}
}
