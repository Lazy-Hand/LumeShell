// Package filepanel shows the remote host's files over the connection's
// SFTP channel. The panel asks the host for directory listings off the
// UI thread and shows them as they come; uploads and downloads run in
// the background and report their progress as toasts.
package filepanel

import (
	"fmt"
	"path"
	"sync"

	"lume/internal/sshclient"
)

// fileChan is what the panel needs of the connection's file channel.
type fileChan interface {
	ReadDir(dir string) ([]sshclient.Entry, error)
	Mkdir(dir string) error
	Remove(path string) error
	Rename(from, to string) error
	Download(remote, dest string) error
	Upload(local, remote string) error
	Close() error
}

// Panel is one session's file browser.
type Panel struct {
	sync.Mutex
	fs        fileChan
	dir       string
	entries   []sshclient.Entry
	loading   map[string]bool // a ReadDir is up for a dir
	err       error           // the last listing's failure
	newDir    string          // the name the new directory dialog takes
	rename    string          // the name the rename dialog edits
	renameTo  string          // what the rename dialog renamed it to
	other     *Panel          // the column across, taking what this sends
	onChanged func()          // how this column redraws itself

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
	}
	return p
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
		}
		changed()
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
// and reports how it went.
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
		}
		changed()
	}()
}

// Remove deletes the entry of the listing named, file or empty dir.
func (p *Panel) Remove(name string, changed func(), say func(title, body string)) {
	go func() {
		if err := p.fs.Remove(join(p.dir, name)); err != nil {
			say(fmt.Sprintf("删除 %s 失败", name), err.Error())
		}
		changed()
	}()
}

// Download copies the remote file to the local path in the background,
// and says how it went.
func (p *Panel) Download(name, local string, say func(title, body string)) {
	remote := join(p.dir, name)
	go func() {
		err := p.fs.Download(remote, local)
		if err != nil {
			say("下载失败", fmt.Sprintf("%s:%v", name, err))
		}
	}()
}

// Upload copies the local file to the directory shown, in the
// background, and says how it went.
func (p *Panel) Upload(name, local string, say func(title, body string)) {
	remote := join(p.dir, name)
	go func() {
		err := p.fs.Upload(local, remote)
		if err != nil {
			say("上传失败", err.Error())
		}
	}()
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
