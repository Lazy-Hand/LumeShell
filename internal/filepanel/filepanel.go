// Package filepanel shows the remote host's files over the connection's
// SFTP channel. The panel asks the host for directory listings off the
// UI thread and shows them as they come; uploads and downloads run in
// the background and report their progress as toasts.
package filepanel

import (
	"fmt"
	"path"
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
	// Send copies this channel's file at src into dst's writer at
	// remote; report hears done of total bytes as they go, nil for none.
	// dst is the other column's channel, reached by its WriterFor.
	Send(src, remote string, dst sshclient.WriterFor, report func(done, total int64)) error
	Close() error
}

// Panel is one session's file browser. IsRemote marks the server side,
// which alone offers 编辑 (在线编辑远端文件).
type Panel struct {
	sync.Mutex
	fs        fileChan
	IsRemote  bool
	dir       string
	entries   []sshclient.Entry
	loading   map[string]bool   // a ReadDir is up for a dir
	err       error             // the last listing's failure
	newDir    string            // the name the new directory dialog takes
	rename    string            // the name the rename dialog edits
	renameTo  string            // what the rename dialog renamed it to
	other     *Panel            // the column across, taking what this sends
	onChanged func()            // how this column redraws itself
	sendSeq   int               // the key of the next transfer
	sends     map[int]*transfer // the transfers in flight, for the view
	queue     chan struct{}     // 并发槽:占住才发,免得大文件互抢带宽

	// 小对话框:新建目录、重命名。kind 说明它为什么开着,dlgValue
	// 是它编辑的名字,dlgOpen 让 Modal 显示它。
	kind     string
	dlgValue string
	dlgOpen  bool
}

// dialog is what the open dialog shows: its title and the buffer it
// edits.
func (p *Panel) dialog() (title, value string) {
	p.Lock()
	defer p.Unlock()
	return p.kind, p.dlgValue
}

// dialogKind is what the open dialog is for: mkdir or rename.
func (p *Panel) dialogKindName() string {
	p.Lock()
	defer p.Unlock()
	return p.kind
}

// dialogOpen says a dialog of the panel is up.
func (p *Panel) dialogOpen() bool {
	p.Lock()
	defer p.Unlock()
	return p.dlgOpen
}

// openDialog stages a dialog: kind says why (mkdir or rename), title
// what it shows.
func (p *Panel) openDialog(kind, title string) {
	p.Lock()
	p.kind, p.dlgValue, p.dlgOpen = kind, "", true
	p.Unlock()
}

// openRename stages the rename dialog for the entry named: it keeps
// `rename` for DoRename, and the input starts with the name as it is.
func (p *Panel) openRename(name string) {
	p.Lock()
	p.rename = name
	p.kind, p.dlgValue, p.dlgOpen = "rename", name, true
	p.Unlock()
}

// closeDialog takes the dialog down without acting on it.
func (p *Panel) closeDialog() {
	p.Lock()
	p.kind, p.dlgValue, p.dlgOpen = "", "", false
	p.Unlock()
}

// dialogFlag is the pointer Modal watches: the flag lives on the panel,
// where closing the dialog by other means (Escape, a click beside it)
// clears it too.
func (p *Panel) dialogFlag() *bool { return &p.dlgOpen }

// dialogBuffer is the pointer the dialog's input edits.
func (p *Panel) dialogBuffer() *string { return &p.dlgValue }

// New makes a panel over the file channel.
func New(fs fileChan, home string) *Panel {
	p := &Panel{
		fs:      fs,
		dir:     home,
		loading: map[string]bool{},
		sends:   map[int]*transfer{},
		queue:   make(chan struct{}, 2), // 同时最多两条传输在跑
	}
	return p
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
// failed one its error, until the listing changes them away.
func (p *Panel) Sends() []transfer {
	p.Lock()
	defer p.Unlock()
	out := make([]transfer, 0, len(p.sends))
	for i := 1; i <= p.sendSeq; i++ {
		if t, ok := p.sends[i]; ok {
			out = append(out, *t)
		}
	}
	return out
}

// ClearSends drops the finished and failed transfers the view shows.
func (p *Panel) ClearSends() {
	p.Lock()
	for k, t := range p.sends {
		if t.err != "" || t.done >= t.total {
			delete(p.sends, k)
		}
	}
	p.Unlock()
}

// Dir returns the directory shown.
func (p *Panel) Dir() string {
	p.Lock()
	defer p.Unlock()
	return p.dir
}

// Entries is the listing shown, sorted with directories first.
func (p *Panel) Entries() []sshclient.Entry {
	p.Lock()
	defer p.Unlock()
	out := make([]sshclient.Entry, len(p.entries))
	copy(out, p.entries)
	return out
}

// Loading says whether the current directory is coming.
func (p *Panel) Loading() bool {
	p.Lock()
	defer p.Unlock()
	return p.loading[p.dir]
}

// Err is the listing's last failure, empty when all is well.
func (p *Panel) Err() string {
	p.Lock()
	defer p.Unlock()
	if p.err == nil {
		return ""
	}
	return p.err.Error()
}

// Open loads the directory and its listing off the UI, and Changed
// redraws the panel as things arrive.
func (p *Panel) Open(dir string, changed func()) {
	p.Lock()
	if p.loading[dir] {
		p.Unlock()
		return
	}
	p.loading[dir] = true
	p.err = nil
	p.Unlock()
	go func() {
		entries, err := p.fs.ReadDir(dir)
		p.Lock()
		delete(p.loading, dir)
		if err != nil {
			p.err = err
			p.Unlock()
		} else {
			sortEntries(entries)
			p.dir, p.entries, p.err = dir, entries, nil
			p.Unlock()
		}
		changed()
	}()
}

// Refresh loads the current directory again.
func (p *Panel) Refresh(changed func()) { p.Open(p.dir, changed) }

// Visit descends into a directory of the listing.
func (p *Panel) Visit(name string, changed func()) {
	p.Open(join(p.dir, name), changed)
}

// Rename renames the entry of the listing named to a new name.
func (p *Panel) Rename(name, to string) error {
	return p.fs.Rename(join(p.dir, name), join(p.dir, to))
}

// DoRename runs the rename this panel has staged, in the background,
// and reports how it went.
func (p *Panel) DoRename(changed func()) {
	p.Lock()
	from, to := p.rename, p.renameTo
	p.rename, p.renameTo = "", ""
	p.Unlock()
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
		p.Open(p.dir, changed)
	}()
}

// Parent goes up one directory, if there is room to.
func (p *Panel) Parent(changed func()) {
	up := path.Dir(p.dir)
	if up != p.dir {
		p.Open(up, changed)
	}
}

// Mkdir makes the staged directory in the one shown, in the background,
// reports how it went, and reloads the listing.
func (p *Panel) Mkdir(changed func()) {
	p.Lock()
	name := p.newDir
	p.newDir = ""
	p.Unlock()
	if name == "" {
		return
	}
	go func() {
		if err := p.fs.Mkdir(join(p.dir, name)); err != nil {
			plistSay("新建目录失败", err.Error())
			if changed != nil {
				changed()
			}
			return
		}
		p.Open(p.dir, changed)
	}()
}

// Remove deletes the entry of the listing named, file or empty dir,
// and reloads the listing once it is gone.
func (p *Panel) Remove(name string, changed func(), say func(title, body string)) {
	go func() {
		if err := p.fs.Remove(join(p.dir, name)); err != nil {
			say(fmt.Sprintf("删除 %s 失败", name), err.Error())
			if changed != nil {
				changed()
			}
			return
		}
		p.Open(p.dir, changed)
	}()
}

// takeIn pulls an entry the other column sends into this one's
// directory: files stream with progress, directories recurse.
func (p *Panel) takeIn(srcPanel *Panel, name string, isDir bool) {
	if isDir {
		p.takeDir(srcPanel, name)
		return
	}
	p.takeFile(srcPanel, name, join(srcPanel.dir, name), join(p.dir, name))
}

// takeFile streams one file from srcPanel's dir into this one's. It
// waits for a free slot in the queue first, so drags beyond the
// concurrency limit line up instead of racing.
func (p *Panel) takeFile(srcPanel *Panel, name, src, dest string) {
	srcPanel.Lock()
	srcFS := srcPanel.fs
	srcPanel.Unlock()
	p.Lock()
	p.sendSeq++
	id := p.sendSeq
	p.sends[id] = &transfer{name: name, total: -1, queued: true}
	changed := p.onChanged
	p.Unlock()
	if changed != nil {
		changed()
	}
	go func() {
		p.queue <- struct{}{}        // 排队拿槽
		defer func() { <-p.queue }() // 传完放出
		p.Lock()
		if t, ok := p.sends[id]; ok {
			t.queued = false
		}
		p.Unlock()
		if changed != nil {
			changed()
		}
		report := func(done, total int64) {
			p.Lock()
			if t, ok := p.sends[id]; ok {
				t.done, t.total = done, total
			}
			p.Unlock()
			if changed != nil {
				changed()
			}
		}
		err := srcFS.Send(src, dest, p.fs, report)
		p.Lock()
		if t, ok := p.sends[id]; ok {
			if err != nil {
				t.err = err.Error()
			} else if t.total < 0 {
				t.total = t.done
			}
		}
		p.Unlock()
		if err != nil {
			plistSay("传输失败", name+": "+err.Error())
			p.kick()
			return
		}
		p.Open(p.dir, changed)
	}()
}

// takeDir recurses a directory of the other column into this one: it
// makes the dir here, then takes each child by its own kind. The top
// dir's progress counts the files as they land.
func (p *Panel) takeDir(srcPanel *Panel, name string) {
	srcPanel.Lock()
	srcFS, srcDir := srcPanel.fs, srcPanel.dir
	srcPanel.Unlock()
	dstDir := join(p.dir, name)
	src := join(srcDir, name)
	p.Lock()
	p.sendSeq++
	id := p.sendSeq
	p.sends[id] = &transfer{name: name + "/", total: -1, queued: true}
	changed := p.onChanged
	p.Unlock()
	if changed != nil {
		changed()
	}
	go func() {
		p.queue <- struct{}{}        // 整棵目录占一个槽
		defer func() { <-p.queue }() // 传完放出
		p.Lock()
		if t, ok := p.sends[id]; ok {
			t.queued = false
		}
		p.Unlock()
		if changed != nil {
			changed()
		}
		// 先在目标建好目录,再一层层把子孙拉过来;任何一个失败
		// 都记到整条传输上,能传多少传多少。
		if err := p.fs.Mkdir(dstDir); err != nil {
			plistSay("新建目录失败", dstDir+": "+err.Error())
			p.kick()
			return
		}
		p.walkDir(srcPanel, srcFS, src, dstDir, id, changed)
		p.Lock()
		if t, ok := p.sends[id]; ok && t.err == "" {
			t.total = t.done // done 累计了文件数,total 跟着落地
		}
		p.Unlock()
		p.Open(p.dir, changed)
	}()
}

// walkDir copies the children of src (on srcFS) into dst (on p.fs),
// counting each file into the transfer's done as it lands.
func (p *Panel) walkDir(srcPanel *Panel, srcFS fileChan, src, dst string, id int, changed func()) {
	ents, err := srcFS.ReadDir(src)
	if err != nil {
		p.Lock()
		if t, ok := p.sends[id]; ok {
			t.err = err.Error()
		}
		p.Unlock()
		return
	}
	for _, e := range ents {
		subSrc := join(src, e.Name)
		subDst := join(dst, e.Name)
		if e.IsDir {
			if err := p.fs.Mkdir(subDst); err != nil {
				p.Lock()
				if t, ok := p.sends[id]; ok {
					t.err = e.Name + ": " + err.Error()
				}
				p.Unlock()
				continue
			}
			p.walkDir(srcPanel, srcFS, subSrc, subDst, id, changed)
			continue
		}
		// 子文件复用 takeFile 的单条传输:名字记相对路径,不占新
		// 条目;进度直接加进外层目录的 done。
		err := srcFS.Send(subSrc, subDst, p.fs, func(done, total int64) {
			p.Lock()
			if t, ok := p.sends[id]; ok {
				t.done++
			}
			p.Unlock()
			if changed != nil {
				changed()
			}
		})
		p.Lock()
		if t, ok := p.sends[id]; ok {
			if err != nil {
				t.err = e.Name + ": " + err.Error()
			} else {
				t.done++
			}
		}
		p.Unlock()
	}
}

// takeDrop pulls a file dragged from the other column into this one's
// directory: a row's Drag carries a dragItem its column drops for.
// Directories stay on their side.
func (p *Panel) takeDrop(e ui.Element) {
	if d, ok := ui.Drop[dragItem](e); ok && d.from != nil && d.from != p {
		d.from.sendOut(d.name, d.isDir)
	}
}

// join joins a directory with a name, remote-style.
func join(dir, name string) string { return dir + "/" + name }

// Close ends the file channel.
func (p *Panel) Close() {
	p.Lock()
	fs := p.fs
	p.Unlock()
	if fs != nil {
		_ = fs.Close()
	}
}
