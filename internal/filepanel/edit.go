package filepanel

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// editOnline pulls a remote file to a temp path, opens it in the
// system editor, and pushes it back once the editor is done with it.
// It runs on its own goroutine; the temp file is removed when the
// editor closes or the app exits.
func (p *Panel) editOnline(name string) {
	p.Lock()
	dir, fs := p.dir, p.fs
	changed := p.onChanged
	p.Unlock()
	src := join(dir, name)
	go func() {
		tmp, err := os.CreateTemp("", "lumeshell-edit-*"+filepath.Ext(name))
		if err != nil {
			plistSay("编辑失败", err.Error())
			return
		}
		local := tmp.Name()
		tmp.Close()
		defer os.Remove(local)
		// 拉下来 → 打开编辑器 → 等它关 → mtime 变就回传。
		if err := fs.Send(src, local, LocalFS{}, nil); err != nil {
			plistSay("编辑失败", name+": "+err.Error())
			return
		}
		mtime := modTime(local)
		if err := openInEditor(local); err != nil {
			plistSay("编辑失败", err.Error())
			return
		}
		// 有的平台 open 立刻返回(Linux),有的等编辑器关(macOS
		// 的 open -W);两边都用轮询 mtime 补,编辑器存盘就回传,
		// 最长盯十分钟,编辑器一直没动就放弃。
		if waitForChange(local, mtime, 10*time.Minute) {
			if err := (LocalFS{}).Send(local, src, fs, nil); err != nil {
				plistSay("回传失败", name+": "+err.Error())
				return
			}
			if changed != nil {
				changed()
			}
		}
	}()
}

// modTime is the file's mtime, zero when it can't be read.
func modTime(path string) time.Time {
	if st, err := os.Stat(path); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

// waitForChange polls path until its mtime passes last or the deadline
// hits; it reports whether a change was seen.
func waitForChange(path string, last time.Time, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if mt := modTime(path); mt.After(last) {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// openInEditor opens path in the system default editor. On macOS the
// `-W` flag waits for the app to close; elsewhere it returns at once,
// and the caller polls for mtime instead.
func openInEditor(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-W", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}
