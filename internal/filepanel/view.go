package filepanel

import (
	"fmt"
	"path/filepath"

	"lume/internal/sshclient"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// base is the last part of a path.
func base(p string) string { return filepath.Base(p) }

// View builds one column of the file browser: its toolbar, its listing
// and its errors. `changed` redraws it with what the background
// goroutines bring in. The listing's rows and the blank space take
// context menus: a row's 进入/发送到对列/重命名/删除, the blank's
// 新建目录/刷新.
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
				for _, e := range p.Entries() {
					entry := e
					isDir := entry.IsDir
					name := entry.Name
					row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(4, 8)
					row.ContextMenu(func(m *ui.Menu) { rowMenu(m, p, entry, changed) })
					row.Children(func() {
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
					})
				}
				// 空白处也收右键:新建目录、刷新。
				ui.Row(c).FillWidth().Grow(1).ContextMenu(func(m *ui.Menu) {
					columnMenu(m, p, changed)
				})
			})
		})
		if p.dialogOpen() {
			p.dialogView(c, changed)
		}
	})
}

// rowMenu fills the context menu of one listing row: 进入 for a
// directory, 发送到对列 for a file, then 重命名 and 删除 for either.
func rowMenu(m *ui.Menu, p *Panel, e sshclient.Entry, changed func()) {
	name := e.Name
	if e.IsDir {
		if m.Item("进入").Chosen() {
			p.Visit(name, changed)
		}
	} else {
		if m.Item("发送到对列").Chosen() {
			p.sendOut(name)
		}
	}
	if m.Item("重命名").Chosen() {
		p.openRename(name)
	}
	m.Separator()
	if m.Item("删除").Chosen() {
		p.Remove(name, changed, plistSay)
	}
}

// columnMenu fills the context menu of the column's blank space:
// 新建目录 asks a name in the small dialog, 刷新 reloads the listing.
func columnMenu(m *ui.Menu, p *Panel, changed func()) {
	if m.Item("新建目录").Chosen() {
		p.openDialog("mkdir", "新建目录")
	}
	if m.Item("刷新").Chosen() {
		p.Refresh(changed)
	}
}

// dialogView is the 新建目录/重命名 dialog: a small modal with a name
// input and 确定/取消, over the column.
func (p *Panel) dialogView(c *ui.Context, changed func()) {
	kind := p.dialogKindName()
	title := map[string]string{"mkdir": "新建目录", "rename": "重命名"}[kind]
	ui.Modal(c, p.dialogFlag(), func() {
		ui.Column(c).Width(280).Gap(10).Padding(16).Background(c.Theme().Background).Radius(10).Children(func() {
			ui.Text(c, title).Bold()
			ui.TextInput(c, p.dialogBuffer()).Placeholder(title).SingleLine()
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "取消").Clicked() {
					p.closeDialog()
				}
				if ui.PrimaryButton(c, "确定").Clicked() {
					p.closeDialogAct(kind, changed)
				}
			})
		})
	})
}

// closeDialogAct takes the dialog down and runs what it confirmed:
// mkdir stages p.newDir for Mkdir, rename p.renameTo for DoRename.
func (p *Panel) closeDialogAct(kind string, changed func()) {
	p.Lock()
	name := p.dlgValue
	switch kind {
	case "mkdir":
		p.newDir = name
	case "rename":
		p.renameTo = name
	}
	p.kind, p.dlgValue, p.dlgOpen = "", "", false
	p.Unlock()
	switch kind {
	case "mkdir":
		p.Mkdir(changed)
	case "rename":
		p.DoRename(changed)
	}
}

// size is what a file's size shows as.
func size(n int64) string {
	const kb, mb = 1 << 10, 1 << 20
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
		// Single-column panel: a save dialog picks where it lands.
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
