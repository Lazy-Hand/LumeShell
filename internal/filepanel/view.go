package filepanel

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// base is the last part of a path.
func base(p string) string { return filepath.Base(p) }

// View builds one column of the file browser: its toolbar, its listing
// and its errors. `changed` redraws it with what the background
// goroutines bring in.
func View(c *ui.Context, p *Panel, changed func()) {
	if p == nil {
		return
	}
	t := c.Theme()
	ui.Column(c).Fill().Background(t.Background).Children(func() {
		ui.Row(c).Gap(6).Padding(8, 10).AlignItems(ui.Center).Children(func() {
			if ui.Button(c, "↑").Clicked() {
				p.Parent(changed)
			}
			ui.Text(c, p.Dir()).FontSize(12).Grow(1).SingleLine().TextColor(t.TextMuted)
			if ui.Button(c, "刷新").Clicked() {
				p.Refresh(changed)
			}
		})
		if msg := p.Err(); msg != "" {
			ui.Row(c).Gap(8).Padding(6, 10).Children(func() {
				ui.Text(c, "读取失败:"+msg).FontSize(12).TextColor(t.Danger).Grow(1)
				if ui.Button(c, "重试").Clicked() {
					p.Refresh(changed)
				}
			})
		} else if p.Loading() {
			ui.Row(c).Gap(8).Padding(6, 10).Children(func() {
				ui.Text(c, "读取中…").FontSize(12).TextColor(t.TextMuted)
			})
		}
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).FillWidth().Padding(4).Gap(2).Children(func() {
				entries := p.Entries()
				for _, e := range entries {
					entry := e
					isDir := entry.IsDir
					name := entry.Name
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(4, 8).Children(func() {
						label := "📄"
						if isDir {
							label = "📁"
						}
						if ui.Row(c).Gap(8).AlignItems(ui.Center).Grow(1).Children(func() {
							ui.Text(c, label)
							ui.Text(c, name).Grow(1).SingleLine()
							if !isDir {
								ui.Text(c, size(entry.Size)).FontSize(11).TextColor(t.TextMuted)
								ui.Text(c, entry.Moded.Format("01-02 15:04")).FontSize(11).TextColor(t.TextMuted)
							}
						}).Clicked() {
							if isDir {
								p.Visit(name, changed)
							}
						}
						if ui.Button(c, "→").Clicked() && !isDir {
							p.sendOut(name)
						}
						if ui.Button(c, "✕").Clicked() {
							p.Remove(name, changed, plistSay)
						}
					})
				}
			})
		})
		ui.Row(c).Gap(6).Padding(8, 10).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &p.newDir).Placeholder("新目录名").Grow(1)
			if ui.Button(c, "新建目录").Clicked() {
				name := p.newDir
				p.newDir = ""
				if name != "" {
					p.Mkdir(name, changed, plistSay)
				}
			}
		})
	})
}

// size is what a file's size shows as.
func size(n int64) string {
	const kb, mb = 1<<10, 1<<20
	switch {
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/mb)
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/kb)
	}
	return fmt.Sprintf("%d B", n)
}

// sendOut hands a file of this column to the other side: the browser
// wires other as the column across, whose directory takes the file.
func (p *Panel) sendOut(name string) {
	p.Lock()
	other := p.other
	dir := p.dir
	p.Unlock()
	if other == nil {
		// 单列面板:退回到保存对话框选址。
		go func() {
			dest, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: "保存 " + name, DefaultPath: name})
			if err != nil || dest == "" {
				return
			}
			if err := p.fs.Download(join(dir, name), dest); err != nil {
				plistSay("传输失败", err.Error())
			}
		}()
		return
	}
	dest := join(other.Dir(), name)
	go func() {
		if err := p.fs.Download(join(dir, name), dest); err != nil {
			plistSay("传输失败", name+": "+err.Error())
			return
		}
		other.kick()
	}()
}

// kick refreshes the column's listing, for a transfer landing in it.
func (p *Panel) kick() {
	p.Lock()
	changed := p.onChanged
	p.Unlock()
	if changed != nil {
		changed()
	}
}

// SetOther wires `other` as the column this one sends files to, and
// changed as how this column redraws itself.
func (p *Panel) SetOther(other *Panel, changed func()) {
	p.Lock()
	p.other, p.onChanged = other, changed
	p.Unlock()
}

// plistSay is how the panel reports transfer failures: mygo.Dialog
// runs its message on its own, from any goroutine.
func plistSay(title, body string) {
	mygo.Dialog.Message(mygo.MessageOptions{Type: mygo.MessageWarning, Title: title, Message: body})
}

// dropAsk picks files from this machine, used by the local column's
// tests only; the browser sends with the 中 button instead.
func dropAsk() []string {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择文件"})
	if err != nil {
		return nil
	}
	return paths
}

// stat says whether a path exists, for tests and wiring.
func stat(p string) (os.FileInfo, error) { return os.Stat(p) }
