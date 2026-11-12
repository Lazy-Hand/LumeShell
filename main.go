// LumeShell is an SSH client: the user picks a host, the app connects and
// shows its terminal, and file transfers ride the same connection. The
// window shows one page at a time: the host list, or a session's
// terminal and its files. Several sessions can be open at once, as
// tabs along the top.
package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"lumeshell/internal/appui"
	"lumeshell/internal/filepanel"
	"lumeshell/internal/profiles"
	"lumeshell/internal/sshclient"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/plugins/updater"
	updaterui "github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/theme"
	"golang.org/x/crypto/ssh"
)

// session is one live connection: its SSH client, terminal, file
// panels, port forwards, and the pages they draw on.
//
// The fields above the blank line are read anywhere. The rest — ending,
// conn, sh, term, and the pages built over them — are guarded by
// app.connMu, so a dial finishing off the UI and a tab closing on it
// never see half of a swap.
type session struct {
	id      string // profile ID + seq, the tab's key; seq orders the tabs
	seq     int
	profile profiles.Profile
	secret  appui.Secret // 内存里,断开自动重连用;不入盘
	watcher *appui.TriggerWatcher
	trigV   *appui.TriggerView
	logger  *sshclient.SessionLogger

	ending   bool // 用户主动断开时置真,sessionEnded 不再重连
	retried  bool // 掉线已经重连过,sessionEnded 不再试
	conn     *sshclient.Client
	sh       *sshclient.Session // the shell, for why it ended
	term     *terminal.Terminal
	termPg   *appui.TermPage
	filesPg  *appui.FilesPage
	tunnelV  *appui.TunnelView
	onFiles  bool             // 它最后停在文件页(换 tab 回来时还原)
	panel    *filepanel.Panel // the remote panel, for closing
	building bool             // its file page is being built
}

// scanConn wraps a session's Conn so what the terminal reads also
// passes the trigger watcher and the session log. Only Read is
// touched; the rest delegates.
type scanConn struct {
	io.ReadWriteCloser
	w   *appui.TriggerWatcher
	log *sshclient.SessionLogger
}

func (s scanConn) Read(p []byte) (int, error) {
	n, err := s.ReadWriteCloser.Read(p)
	if n > 0 {
		s.w.Scan(p[:n])
		s.log.Write(p[:n])
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

	// connMu guards the sessions, the mutable fields of each, and which
	// one the hub shows.
	connMu sync.Mutex
	seq    int
	active string
	sess   map[string]*session

	guard   dialGuard
	hostKey ssh.HostKeyCallback // set once the window is up
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
	// Sparkle 式更新窗口:后台每日检查,"检查更新…"在应用菜单里。
	// dev 构建下是 no-op。
	mygo.Use(updaterui.Plugin)
	mygo.App.WhenReady(func() {
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Label: "LumeShell", Submenu: []*mygo.MenuItem{
				{Role: mygo.RoleAbout},
				updater.MenuItem(),
				mygo.Separator(),
				{Role: mygo.RoleServices},
				mygo.Separator(),
				{Role: mygo.RoleHide},
				{Role: mygo.RoleHideOthers},
				{Role: mygo.RoleUnhide},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			}},
			{Role: mygo.RoleEditMenu},
			{Role: mygo.RoleWindowMenu},
		}))
		mygo.Theme.SetSource(appui.LoadThemeSource())
		dir, err := mygo.App.Path(mygo.PathUserData)
		if err == nil {
			a.store, listErr = profiles.Open(dir)
		}
		a.list = appui.NewList(a.store)
		a.list.Connect = a.connect
		a.list.Delete = func(p profiles.Profile) { _ = a.store.Remove(p.ID) }
		a.list.LocalShell = a.openLocalShell
		a.hub.List = a.list
		a.hub.Now = appui.Home
		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "LumeShell",
			Width:     960,
			Height:    640,
			MinWidth:  720,
			MinHeight: 480,
			StateKey:  "main",
			Content:   ui.View(a.view),
		})
		a.hub.Win = a.win
		// A host key the file does not know is offered to the user, who
		// decides whether it is saved; one that disagrees is refused.
		a.hostKey = sshclient.HostKeys(hostKeyAsk(a.win))
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

// tabs draws a tab per open session plus a "+" that goes back to the
// host list: clicking one brings its page up, the ✕ closes it. With
// nothing open it renders nothing — the list page is the view then.
func (a *app) tabs(c *ui.Context) {
	sessions, active := a.orderedSessions()
	if len(sessions) == 0 {
		return
	}
	tk := theme.TokensOf(c)
	ui.Row(c).Gap(2).Padding(4, 8).AlignItems(ui.Center).Background(tk.BgContainer).Children(func() {
		for _, s := range sessions {
			sess := s
			on := sess.id == active
			btn := ui.Row(c).Key(sess.id).Gap(6).Padding(4, 10).Radius(tk.BorderRadiusSM).Cursor(ui.CursorPointer).
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
		// + :回到主机列表再连一台,已有 tab 留着。
		plus := ui.Row(c).Padding(4, 10).Radius(tk.BorderRadiusSM).Cursor(ui.CursorPointer)
		if plus.Hovered() {
			plus.Background(tk.FillQuaternary)
		}
		plus.Children(func() {
			ui.Icon(c, lucide.Plus()).FontSize(14).TextColor(tk.TextSecondary)
		})
		if plus.Clicked() {
			a.showList()
		}
	})
}

// orderedSessions is the open sessions in the order their tabs opened,
// and the one the hub shows: a map has no order of its own.
func (a *app) orderedSessions() ([]*session, string) {
	a.connMu.Lock()
	sessions := make([]*session, 0, len(a.sess))
	for _, s := range a.sess {
		sessions = append(sessions, s)
	}
	active := a.active
	a.connMu.Unlock()
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].seq < sessions[j].seq })
	return sessions, active
}

// showList brings the host list up without dropping any session.
func (a *app) showList() {
	a.win.Update(func() {
		a.hub.Now = appui.Home
		a.hub.Tunnels = nil // 没有会话的页面,就没有它的隧道弹窗
	})
}

// openLocalShell opens a tab running the user's login shell on this
// machine — no SSH, no SFTP, no tunnels. Its session carries a nil
// conn; every conn use checks for it.
func (a *app) openLocalShell() {
	a.connMu.Lock()
	a.seq++
	id := fmt.Sprintf("local-%d", a.seq)
	seq := a.seq
	a.connMu.Unlock()
	ss := &session{id: id, seq: seq, profile: profiles.Profile{Label: "本地终端"}}
	ss.watcher = appui.NewTriggerWatcher(func(matched string) { a.triggerHit(id, matched) })
	ss.trigV = appui.NewTriggerView(ss.watcher)
	ss.logger = sshclient.OpenSessionLogger(a.logDir(), "local")
	term, err := terminal.New(terminal.Options{
		OptionAsAlt: true,
		// 空 Command 走 $SHELL 登录 shell;Conn 空就是本地 PTY。
		OnTitle: func(title string) { a.win.SetTitle(title) },
		OnExit:  func(int) { a.sessionEnded(id) },
	})
	if err != nil {
		ss.logger.Close()
		return
	}
	ss.term = term
	ss.termPg = &appui.TermPage{
		Profile:  ss.profile,
		Term:     term,
		Snippets: appui.NewSnippetView(),
		Triggers: ss.trigV,
		Back:     func() { a.leaveSession(id) },
		// ToFiles/Tunnels 留空:本地会话没有远端文件和隧道。
	}
	a.connMu.Lock()
	a.sess[id] = ss
	a.active = id
	a.connMu.Unlock()
	a.win.Update(func() {
		a.hub.Term = ss.termPg
		a.hub.Tunnels = nil
		a.hub.Now = appui.Session
	})
}

// connect starts a session with the profile and secret the list page
// gave. The dial runs off the UI; the terminal comes up when the
// connection is there.
func (a *app) connect(p profiles.Profile, s appui.Secret) {
	if !a.guard.try() {
		return // 已经有一次在拨号,列表页也已经在说"连接中"
	}
	a.list.SetConnecting(true)
	go func() {
		defer a.guard.done()
		conn, sh, err := a.dial(p, s)
		if err != nil {
			a.failConnect(appui.ErrorText(err))
			return
		}
		_ = a.store.Touch(p.ID)
		a.connMu.Lock()
		a.seq++
		id := fmt.Sprintf("%s-%d", p.ID, a.seq)
		seq := a.seq
		a.connMu.Unlock()
		ss := &session{id: id, seq: seq, profile: p, secret: s, conn: conn, sh: sh}
		ss.watcher = appui.NewTriggerWatcher(func(matched string) { a.triggerHit(id, matched) })
		ss.trigV = appui.NewTriggerView(ss.watcher)
		ss.logger = sshclient.OpenSessionLogger(a.logDir(), sanitizeLogName(p.Title()))
		var tconn io.ReadWriteCloser = sh
		if ss.watcher.HasPatterns() || ss.logger.Path() != "" {
			tconn = scanConn{ReadWriteCloser: sh, w: ss.watcher, log: ss.logger}
		}
		term, err := terminal.New(terminal.Options{
			Conn:        tconn,
			OptionAsAlt: true,
			OnTitle:     func(title string) { a.win.SetTitle(title) },
			OnExit:      func(int) { a.sessionEnded(id) },
		})
		if err != nil {
			ss.logger.Close()
			sh.Close()
			conn.Close()
			a.failConnect(appui.ErrorText(err))
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
			NewConn:  func() { a.showList() },
		}
		a.connMu.Lock()
		a.sess[id] = ss
		a.active = id
		a.connMu.Unlock()
		a.win.Update(func() {
			a.list.SetConnecting(false)
			a.hub.Term = ss.termPg
			a.hub.Tunnels = nil
			a.hub.Now = appui.Session
		})
	}()
}

// dial opens the connection and its shell for the profile, off the UI
// thread: what connect and reconnect both need.
func (a *app) dial(p profiles.Profile, s appui.Secret) (*sshclient.Client, *sshclient.Session, error) {
	auth, err := authOf(p, s)
	if err != nil {
		return nil, nil, err
	}
	conf := sshclient.Config{Host: p.Host, Port: p.Port, User: p.User, Auth: auth, HostKey: a.hostKey}
	if p.JumpHost != "" {
		conf.Jump = &sshclient.Jump{Host: p.JumpHost, Port: p.JumpPort, User: p.JumpUser, Auth: auth}
	}
	conn, err := sshclient.Connect(conf)
	if err != nil {
		return nil, nil, err
	}
	sh, err := conn.Shell(80, 24)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, sh, nil
}

// filesPage opens the file browser of session id, building it the first
// time over that connection.
func (a *app) filesPage(id string) {
	a.connMu.Lock()
	ss, ok := a.sess[id]
	if !ok {
		a.connMu.Unlock()
		return
	}
	page, conn := ss.filesPg, ss.conn
	if page != nil {
		ss.onFiles = true
		a.connMu.Unlock()
		a.win.Update(func() {
			a.hub.Files = page
			a.hub.Tunnels = nil
			a.hub.Now = appui.Files
		})
		return
	}
	if conn == nil || ss.building {
		a.connMu.Unlock()
		return // 本地 shell 没有 SFTP;或者页面正在建
	}
	ss.building = true
	profile := ss.profile
	a.connMu.Unlock()
	go func() {
		defer func() {
			a.connMu.Lock()
			if cur, ok := a.sess[id]; ok {
				cur.building = false
			}
			a.connMu.Unlock()
		}()
		fs, err := conn.SFTP()
		if err != nil {
			a.win.Update(func() { a.list.FailedCard("文件服务不可用:" + appui.ErrorText(err)) })
			return
		}
		home, err := fs.Home()
		if err != nil {
			home = "/"
		}
		changed := a.redraw()
		remote := filepanel.New(fs, home)
		remote.IsRemote = true
		localDir := filepanel.LocalHome()
		local := filepanel.New(filepanel.LocalFS{}, localDir)
		remote.SetOther(local, changed)
		local.SetOther(remote, changed)
		remote.Open(home, changed)
		local.Open(localDir, changed)
		page := &appui.FilesPage{
			Profile:   profile,
			Remote:    remote,
			Local:     local,
			SideWidth: 420,
			Back:      func() { a.backToTerm(id) },
		}
		a.connMu.Lock()
		cur, live := a.sess[id]
		if !live {
			a.connMu.Unlock()
			remote.Close() // the tab closed while the channel opened
			return
		}
		cur.filesPg, cur.panel, cur.onFiles = page, remote, true
		active := a.active == id
		a.connMu.Unlock()
		if active {
			a.win.Update(func() {
				a.hub.Files = page
				a.hub.Tunnels = nil
				a.hub.Now = appui.Files
			})
		}
	}()
}

// redraw returns a callback that redraws the window at most once a
// frame. Transfers report progress every chunk, and a rebuild per chunk
// would spend the UI on progress bars; the last report of a burst still
// gets its frame.
func (a *app) redraw() func() {
	const every = 33 * time.Millisecond
	win := a.win
	var mu sync.Mutex
	var last time.Time
	var pending bool
	return func() {
		mu.Lock()
		if time.Since(last) < every {
			if pending {
				mu.Unlock()
				return
			}
			pending = true
			mu.Unlock()
			time.AfterFunc(every, func() {
				mu.Lock()
				last, pending = time.Now(), false
				mu.Unlock()
				win.Invalidate()
			})
			return
		}
		last = time.Now()
		mu.Unlock()
		win.Invalidate()
	}
}

// showSession brings session id's page up: its files when it was last
// on them, its terminal otherwise.
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
	a.win.Update(func() { a.showNow(id) })
}

// showNow points the hub at id's page; it runs on the main thread.
func (a *app) showNow(id string) {
	a.connMu.Lock()
	ss, ok := a.sess[id]
	var term *appui.TermPage
	var files *appui.FilesPage
	var tunnels *appui.TunnelView
	onFiles := false
	if ok {
		tunnels = ss.tunnelV
		term, files, onFiles = ss.termPg, ss.filesPg, ss.onFiles
	}
	a.connMu.Unlock()
	switch {
	case !ok:
		a.hub.Term, a.hub.Files, a.hub.Tunnels = nil, nil, nil
		a.hub.Now = appui.Home
	case files != nil && onFiles:
		a.hub.Term, a.hub.Files, a.hub.Tunnels = term, files, tunnels
		a.hub.Now = appui.Files
	default:
		a.hub.Term, a.hub.Files, a.hub.Tunnels = term, files, tunnels
		a.hub.Now = appui.Session
	}
}

// backToTerm leaves the file browser for the terminal the session was
// opened on.
func (a *app) backToTerm(id string) {
	a.connMu.Lock()
	if ss, ok := a.sess[id]; ok {
		ss.onFiles = false
	}
	a.connMu.Unlock()
	a.showSession(id)
}

// failConnect reports why a session did not come up. It leaves the page
// the user is on alone: a failed dial is no reason to leave a session.
func (a *app) failConnect(why string) {
	a.win.Update(func() {
		a.list.SetConnecting(false)
		a.list.FailedCard(why)
		a.list.ShowHome()
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

// sessionEnded runs when the terminal reports the connection's end: a
// shell the user exited ends its tab, and a connection that dropped is
// brought back once.
func (a *app) sessionEnded(id string) {
	a.connMu.Lock()
	ss, ok := a.sess[id]
	wasActive := ok && a.active == id
	var ending, retried bool
	var sh *sshclient.Session
	if ok {
		ending, retried, sh = ss.ending, ss.retried, ss.sh
	}
	a.connMu.Unlock()
	if !ok || ending {
		return
	}
	// A shell the user typed exit in ended cleanly; only a connection
	// that dropped is worth bringing back.
	if !droppedShell(sh) {
		a.closeSession(id)
		if wasActive {
			a.win.Update(func() { a.win.SetTitle("LumeShell") })
		}
		return
	}
	if !retried {
		go func() {
			if a.reconnect(ss) {
				return
			}
			a.endSession(ss, wasActive)
		}()
		return
	}
	go a.endSession(ss, wasActive)
}

// endSession reports a session that is over and closes its tab. It runs
// off the UI thread, where the dialog it shows may block.
func (a *app) endSession(ss *session, wasActive bool) {
	mygo.Dialog.Message(mygo.MessageOptions{
		Title:   "连接已断开",
		Message: ss.profile.Title() + " 的会话已结束",
	})
	a.closeSession(ss.id)
	if wasActive {
		a.win.Update(func() { a.win.SetTitle("LumeShell") })
	}
}

// reconnect brings a dead session back: same profile, same secret, a
// fresh connection and terminal swapped into the same tab, the old ones
// closed. It reports whether the session is live again.
func (a *app) reconnect(ss *session) bool {
	conn, sh, err := a.dial(ss.profile, ss.secret)
	if err != nil {
		return false
	}
	var tconn io.ReadWriteCloser = sh
	if ss.watcher.HasPatterns() || ss.logger.Path() != "" {
		tconn = scanConn{ReadWriteCloser: sh, w: ss.watcher, log: ss.logger}
	}
	term, err := terminal.New(terminal.Options{
		Conn:        tconn,
		OptionAsAlt: true,
		OnTitle:     func(title string) { a.win.SetTitle(title) },
		OnExit:      func(int) { a.sessionEnded(ss.id) },
	})
	if err != nil {
		sh.Close()
		conn.Close()
		return false
	}
	tunnels := appui.NewTunnelView(conn)
	page := &appui.TermPage{
		Profile:  ss.profile,
		Term:     term,
		Snippets: appui.NewSnippetView(),
		Triggers: ss.trigV,
		Back:     func() { a.leaveSession(ss.id) },
		ToFiles:  func() { a.filesPage(ss.id) },
		Tunnels:  func() { tunnels.Open() },
		NewConn:  func() { a.showList() },
	}
	a.connMu.Lock()
	if cur, ok := a.sess[ss.id]; !ok || cur != ss {
		// The tab closed while the dial was up: the new parts have no
		// page to land on.
		a.connMu.Unlock()
		term.Close()
		conn.Close()
		return false
	}
	oldTerm, oldConn, oldPanel := ss.term, ss.conn, ss.panel
	ss.conn, ss.sh, ss.term, ss.termPg, ss.tunnelV = conn, sh, term, page, tunnels
	ss.filesPg, ss.panel, ss.onFiles, ss.building = nil, nil, false, false
	ss.retried = true
	active := a.active == ss.id
	a.connMu.Unlock()
	// The old parts go now: its terminal, the SFTP channel over its
	// connection, and the connection itself (and its keepalive).
	if oldTerm != nil {
		oldTerm.Close()
	}
	if oldPanel != nil {
		oldPanel.Close()
	}
	if oldConn != nil {
		oldConn.Close()
	}
	if active {
		a.win.Update(func() {
			a.hub.Term = page
			a.hub.Files = nil
			a.hub.Tunnels = tunnels
			a.hub.Now = appui.Session
		})
	}
	return true
}

// logDir is where session logs go: logs/ beside the profiles file.
func (a *app) logDir() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "logs")
}

// sanitizeLogName keeps a session title safe inside a filename.
func sanitizeLogName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r >= 0x80: // 汉字等放行
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
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
	next := ""
	wasActive := ok && a.active == id
	var term *terminal.Terminal
	var panel *filepanel.Panel
	var logger *sshclient.SessionLogger
	var conn *sshclient.Client
	if ok {
		delete(a.sess, id)
		if wasActive {
			a.active = a.nextTabLocked(ss.seq)
		}
		next = a.active
		// Take the parts under the lock: a reconnect finishing at the
		// same moment would otherwise swap them out from under us.
		term, panel, logger, conn = ss.term, ss.panel, ss.logger, ss.conn
	}
	a.connMu.Unlock()
	if !ok {
		return
	}
	if term != nil {
		term.Close()
	}
	if panel != nil {
		panel.Close()
	}
	if logger != nil {
		logger.Close()
	}
	if conn != nil {
		conn.Close()
	}
	if !wasActive {
		return // 关的是别的 tab,当前页不动
	}
	a.win.Update(func() {
		if next == "" {
			a.hub.Term, a.hub.Files, a.hub.Tunnels = nil, nil, nil
			a.hub.Now = appui.Home
			return
		}
		a.showNow(next)
	})
}

// nextTabLocked picks the tab to show after the one at seq closed: its
// neighbour to the right, else the last one left. connMu is held.
func (a *app) nextTabLocked(seq int) string {
	var after, before *session
	for _, s := range a.sess {
		if s.seq == seq {
			continue // the tab being closed
		}
		if s.seq > seq {
			if after == nil || s.seq < after.seq {
				after = s
			}
		} else if before == nil || s.seq > before.seq {
			before = s
		}
	}
	if after != nil {
		return after.id
	}
	if before != nil {
		return before.id
	}
	return ""
}

// teardownAll ends every session.
func (a *app) teardownAll() {
	a.connMu.Lock()
	sessions := a.sess
	a.sess = map[string]*session{}
	a.active = ""
	a.connMu.Unlock()
	for _, ss := range sessions {
		if ss.term != nil {
			ss.term.Close()
		}
		if ss.panel != nil {
			ss.panel.Close()
		}
		if ss.logger != nil {
			ss.logger.Close()
		}
		if ss.conn != nil {
			ss.conn.Close()
		}
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

// hostKeyAsk offers an unknown host's key to the user, who decides
// whether it is trusted and saved. It runs on the connecting goroutine,
// where a blocking dialog is fine.
func hostKeyAsk(win *mygo.Window) func(host string, key ssh.PublicKey) bool {
	return func(host string, key ssh.PublicKey) bool {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:        win,
			Type:          mygo.MessageQuestion,
			Title:         "新的主机密钥",
			Message:       host + " 的主机密钥不在 known_hosts 里",
			Detail:        ssh.FingerprintSHA256(key),
			Buttons:       []string{"信任并保存", "取消"},
			DefaultButton: 1,
			CancelButton:  1,
		})
		return err == nil && res.Button == 0
	}
}

// droppedShell reports whether a shell's end was the connection's, not
// a shell the user exited: a clean exit has no error, and an exit with
// a status is still the shell's own.
func droppedShell(sh *sshclient.Session) bool {
	if sh == nil {
		return false
	}
	err := sh.EndErr()
	if err == nil {
		return false
	}
	var exit *ssh.ExitError
	return !errors.As(err, &exit)
}
