// Package filepanel shows the remote host's files over the connection's
// SFTP channel. The panel asks the host for directory listings off the
// UI thread and shows them as they come; uploads and downloads run in
// the background and report their progress as toasts.
package filepanel

import (
	"fmt"
	"path"
	"sort"
	"sync"

	"lumeshell/internal/sshclient"

	"github.com/egoist/mygo/ui"
)

// fileChan is what the panel needs of the connection's file channel.
// Send is the one copy a transfer needs: the source channel streams its
// file at src into a writer the destination channel makes at remote.
type fileChan interface {
	sshclient.WriterFor
	ReadDir(dir string) ([]sshclient.Entry, error)
	Mkdir(dir string) error
	Remove(path string) error
	Rename(from, to string) error
	// Join joins a directory with a name the way the channel's file
	// system does.
	Join(dir, name string) string
	// Send copies this channel's file at src into dst's writer at
	// remote; report hears done of total bytes as they go, nil for none.
	// dst is the other column's channel, reached by its WriterFor.
	Send(src, remote string, dst sshclient.WriterFor, report func(done, total int64)) error
	Close() error
}

// Panel is one session's file browser. IsRemote marks the server side,
// which alone offers 编辑 (在线编辑远端文件).
//
// Every field behind mu is touched from background goroutines: the
// listing they load, the transfers they run, and the counts they report.
// The dialog fields are the UI thread's alone.
type Panel struct {
	mu        sync.Mutex
	fs        fileChan
	IsRemote  bool
	dir       string
	entries   []sshclient.Entry
	loading   map[string]bool // a ReadDir is up for a dir
	errStr    string          // the last listing's failure
	gen       int             // grows per Open; older results are dropped
	newDir    string          // the name Mkdir takes
	rename    string          // the name DoRename renames
	renameTo  string          // what it renames it to
	other     *Panel          // the column across, taking what this sends
	onChanged func()          // how this column redraws itself
	sendSeq   int             // the key of the next transfer
	sends     map[int]*transfer
	queue     chan struct{} // 并发槽:占住才发,免得大文件互抢带宽

	// 小对话框:新建目录、重命名。kind 说明它为什么开着,dlgValue
	// 是它编辑的名字,dlgOpen 让 Modal 显示它。只由 UI 线程读写。
	kind     string
	dlgValue string
	dlgOpen  bool
}

// dialogKindName is what the open dialog is for: mkdir or rename.
func (p *Panel) dialogKindName() string { return p.kind }

// dialogOpen says a dialog of the panel is up.
func (p *Panel) dialogOpen() bool { return p.dlgOpen }

// openDialog stages a dialog: kind says why (mkdir or rename).
func (p *Panel) openDialog(kind, title string) {
	p.kind, p.dlgValue, p.dlgOpen = kind, "", true
}

// openRename stages the rename dialog for the entry named: it keeps
// `rename` for DoRename, and the input starts with the name as it is.
func (p *Panel) openRename(name string) {
	p.mu.Lock()
	p.rename = name
	p.mu.Unlock()
	p.kind, p.dlgValue, p.dlgOpen = "rename", name, true
}

// closeDialog takes the dialog down without acting on it.
func (p *Panel) closeDialog() {
	p.kind, p.dlgValue, p.dlgOpen = "", "", false
}

// dialogFlag is the pointer Modal watches: the flag lives on the panel,
// where closing the dialog by other means (Escape, a click beside it)
// clears it too.
func (p *Panel) dialogFlag() *bool { return &p.dlgOpen }

// dialogBuffer is the pointer the dialog's input edits.
func (p *Panel) dialogBuffer() *string { return &p.dlgValue }

// New makes a panel over the file channel.
func New(fs fileChan, home string) *Panel {
	return &Panel{
		fs:      fs,
		dir:     home,
		loading: map[string]bool{},
		sends:   map[int]*transfer{},
		queue:   make(chan struct{}, 2), // 同时最多两条传输在跑
	}
}

// transfer is one send landing on a panel: its name, how much of it has
// arrived, and the total, or a failure once it ends wrong. queued marks
// one still waiting for a slot; the view shows it as 排队中. The view
// draws one progress line for each.
type transfer struct {
	name   string
	done   int64
	total  int64
	err    string
	queued bool
}

// dragItem is what a row's Drag carries: the panel it leaves, the
// entry's name, and whether it is a directory (目录走递归传输).
type dragItem struct {
	from  *Panel
	name  string
	isDir bool
}

// Sends is the transfers in flight, in the order they started: the view
// draws one progress line for each. A finished one keeps a full bar, a
// failed one its error, until the next transfer starts.
func (p *Panel) Sends() []transfer {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]int, 0, len(p.sends))
	for id := range p.sends {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]transfer, 0, len(ids))
	for _, id := range ids {
		out = append(out, *p.sends[id])
	}
	return out
}

// dropFinishedLocked forgets the transfers that ended. p.mu is held.
func (p *Panel) dropFinishedLocked() {
	for k, t := range p.sends {
		if t.err != "" || (t.total >= 0 && t.done >= t.total) {
			delete(p.sends, k)
		}
	}
}

// Dir returns the directory shown.
func (p *Panel) Dir() string { return p.dirNow() }

func (p *Panel) dirNow() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dir
}

// Entries is the listing shown, sorted with directories first.
func (p *Panel) Entries() []sshclient.Entry {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]sshclient.Entry, len(p.entries))
	copy(out, p.entries)
	return out
}

// Loading says whether the current directory is coming.
func (p *Panel) Loading() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loading[p.dir]
}

// Err is the listing's last failure, empty when all is well.
func (p *Panel) Err() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.errStr
}

// Open loads the directory and its listing off the UI, and changed
// redraws the panel as things arrive. A listing that a newer Open has
// overtaken is dropped, so a slow directory never rolls the panel back
// to what the user left.
func (p *Panel) Open(dir string, changed func()) {
	p.mu.Lock()
	if p.loading[dir] {
		p.mu.Unlock()
		return
	}
	p.loading[dir] = true
	p.gen++
	gen := p.gen
	p.mu.Unlock()
	go func() {
		entries, err := p.fs.ReadDir(dir)
		p.mu.Lock()
		delete(p.loading, dir)
		if p.gen != gen {
			p.mu.Unlock()
			return // a newer Open is on its way
		}
		if err != nil {
			p.errStr = err.Error()
		} else {
			sortEntries(entries)
			p.dir, p.entries, p.errStr = dir, entries, ""
		}
		p.mu.Unlock()
		if changed != nil {
			changed()
		}
	}()
}

// Refresh loads the current directory again.
func (p *Panel) Refresh(changed func()) { p.Open(p.dirNow(), changed) }

// Visit descends into a directory of the listing.
func (p *Panel) Visit(name string, changed func()) {
	p.Open(p.join(p.dirNow(), name), changed)
}

// Rename renames the entry of the listing named to a new name.
func (p *Panel) Rename(name, to string) error {
	dir := p.dirNow()
	return p.fs.Rename(p.join(dir, name), p.join(dir, to))
}

// DoRename runs the rename this panel has staged, in the background,
// and reports how it went.
func (p *Panel) DoRename(changed func()) {
	p.mu.Lock()
	from, to := p.rename, p.renameTo
	p.rename, p.renameTo = "", ""
	p.mu.Unlock()
	if from == "" || to == "" || to == from {
		return
	}
	go func() {
		if err := p.Rename(from, to); err != nil {
			plistSay(fmt.Sprintf("重命名 %s 失败", from), err.Error())
			if changed != nil {
				changed()
			}
			return
		}
		p.Open(p.dirNow(), changed)
	}()
}

// Parent goes up one directory, if there is room to.
func (p *Panel) Parent(changed func()) {
	dir := p.dirNow()
	up := path.Dir(dir)
	if up != dir {
		p.Open(up, changed)
	}
}

// Mkdir makes the staged directory in the one shown, in the background,
// reports how it went, and reloads the listing.
func (p *Panel) Mkdir(changed func()) {
	p.mu.Lock()
	name, dir := p.newDir, p.dir
	p.newDir = ""
	p.mu.Unlock()
	if name == "" {
		return
	}
	go func() {
		if err := p.fs.Mkdir(p.join(dir, name)); err != nil {
			plistSay("新建目录失败", err.Error())
			if changed != nil {
				changed()
			}
			return
		}
		p.Open(dir, changed)
	}()
}

// Remove deletes the entry of the listing named, file or empty dir, and
// reloads the listing once it is gone.
func (p *Panel) Remove(name string, changed func(), say func(title, body string)) {
	dir := p.dirNow()
	go func() {
		if err := p.fs.Remove(p.join(dir, name)); err != nil {
			say(fmt.Sprintf("删除 %s 失败", name), err.Error())
			if changed != nil {
				changed()
			}
			return
		}
		p.Open(dir, changed)
	}()
}

// join joins a directory with a name the way the panel's file system
// does: the remote server's style, or this machine's.
func (p *Panel) join(dir, name string) string { return p.fs.Join(dir, name) }

// takeIn pulls an entry the other column sends into this one's
// directory: files stream with progress, directories recurse.
func (p *Panel) takeIn(srcPanel *Panel, name string, isDir bool) {
	srcDir := srcPanel.dirNow()
	dstDir := p.dirNow()
	if isDir {
		p.takeDir(srcPanel, name, srcDir, dstDir)
		return
	}
	p.takeFile(srcPanel, name, srcPanel.join(srcDir, name), p.join(dstDir, name))
}

// takeFile streams one file from srcPanel's dir into this one's. It
// waits for a free slot in the queue first, so drags beyond the
// concurrency limit line up instead of racing.
func (p *Panel) takeFile(srcPanel *Panel, name, src, dest string) {
	p.mu.Lock()
	srcFS := srcPanel.fs
	p.dropFinishedLocked()
	p.sendSeq++
	id := p.sendSeq
	p.sends[id] = &transfer{name: name, total: -1, queued: true}
	changed := p.onChanged
	p.mu.Unlock()
	if changed != nil {
		changed()
	}
	go func() {
		p.queue <- struct{}{}        // 排队拿槽
		defer func() { <-p.queue }() // 传完放出
		p.mu.Lock()
		if t, ok := p.sends[id]; ok {
			t.queued = false
		}
		p.mu.Unlock()
		if changed != nil {
			changed()
		}
		report := func(done, total int64) {
			p.mu.Lock()
			if t, ok := p.sends[id]; ok {
				t.done, t.total = done, total
			}
			p.mu.Unlock()
			if changed != nil {
				changed()
			}
		}
		err := srcFS.Send(src, dest, p.fs, report)
		p.mu.Lock()
		if t, ok := p.sends[id]; ok {
			if err != nil {
				t.err = err.Error()
			} else if t.total < 0 {
				t.total = t.done
			}
		}
		p.mu.Unlock()
		if err != nil {
			plistSay("传输失败", name+": "+err.Error())
			p.kick()
			return
		}
		p.Open(p.dirNow(), changed)
	}()
}

// takeDir recurses a directory of the other column into this one: it
// makes the dir here, then takes each child by its own kind. The
// transfer counts files, so the bar shows how many of them landed once
// the whole tree is known.
func (p *Panel) takeDir(srcPanel *Panel, name, srcDir, dstDir string) {
	p.mu.Lock()
	srcFS := srcPanel.fs
	p.dropFinishedLocked()
	p.sendSeq++
	id := p.sendSeq
	p.sends[id] = &transfer{name: name + "/", total: -1, queued: true}
	changed := p.onChanged
	p.mu.Unlock()
	if changed != nil {
		changed()
	}
	dir := p.join(dstDir, name)
	src := p.join(srcDir, name)
	go func() {
		p.queue <- struct{}{}        // 整棵目录占一个槽
		defer func() { <-p.queue }() // 传完放出
		p.mu.Lock()
		if t, ok := p.sends[id]; ok {
			t.queued = false
		}
		p.mu.Unlock()
		if changed != nil {
			changed()
		}
		// 先在目标建好目录,再一层层把子孙拉过来;任何一个失败
		// 都记到整条传输上,能传多少传多少。
		if err := p.fs.Mkdir(dir); err != nil {
			plistSay("新建目录失败", dir+": "+err.Error())
			p.setSendErr(id, err.Error())
			p.kick()
			return
		}
		p.walkDir(srcFS, src, dir, id, changed)
		p.mu.Lock()
		if t, ok := p.sends[id]; ok && t.err == "" {
			t.total = t.done // done 数的是文件,total 跟着落地
		}
		p.mu.Unlock()
		p.Open(p.dirNow(), changed)
	}()
}

// walkDir copies the children of src (on srcFS) into dst (on p.fs),
// counting each file into the transfer's done as it lands.
func (p *Panel) walkDir(srcFS fileChan, src, dst string, id int, changed func()) {
	ents, err := srcFS.ReadDir(src)
	if err != nil {
		p.setSendErr(id, err.Error())
		return
	}
	for _, e := range ents {
		subSrc := srcFS.Join(src, e.Name)
		subDst := p.join(dst, e.Name)
		if e.IsDir {
			if err := p.fs.Mkdir(subDst); err != nil {
				p.setSendErr(id, e.Name+": "+err.Error())
				continue
			}
			p.walkDir(srcFS, subSrc, subDst, id, changed)
			continue
		}
		// 子文件由源通道流进目标通道,进度只数落地的文件,不数块。
		if err := srcFS.Send(subSrc, subDst, p.fs, nil); err != nil {
			p.setSendErr(id, e.Name+": "+err.Error())
			continue
		}
		p.mu.Lock()
		if t, ok := p.sends[id]; ok {
			t.done++
		}
		p.mu.Unlock()
		if changed != nil {
			changed()
		}
	}
}

// setSendErr records a failure on a transfer.
func (p *Panel) setSendErr(id int, msg string) {
	p.mu.Lock()
	if t, ok := p.sends[id]; ok {
		t.err = msg
	}
	p.mu.Unlock()
}

// takeDrop pulls a file dragged from the other column into this one's
// directory: a row's Drag carries a dragItem its column drops for.
// Directories stay on their side.
func (p *Panel) takeDrop(e ui.Element) {
	if d, ok := ui.Drop[dragItem](e); ok && d.from != nil && d.from != p {
		d.from.sendOut(d.name, d.isDir)
	}
}

// Close ends the file channel.
func (p *Panel) Close() {
	p.mu.Lock()
	fs := p.fs
	p.mu.Unlock()
	if fs != nil {
		_ = fs.Close()
	}
}
