package filepanel

import (
	"fmt"
	"path/filepath"
	"time"

	"lumeshell/internal/sshclient"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/layout"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/overlay"
	"github.com/lobehub/lobe-mygo/pkg/theme"
)

// base is the last part of a path.
func base(p string) string { return filepath.Base(p) }

// View builds one column of the file browser: its toolbar, its listing
// and its errors. `changed` redraws it with what the background
// goroutines bring in; nil falls back to the panel's own onChanged,
// which the two-column browser wires and single columns leave alone.
// The listing's rows and the blank space take context menus: a row's
// 进入/发送到对列/重命名/删除, the blank's 新建目录/刷新.
func View(c *ui.Context, p *Panel, changed func()) {
	if p == nil {
		return
	}
	if changed == nil {
		changed = p.kick
	}
	tk := theme.TokensOf(c)
	root := ui.Column(c).Fill().Background(tk.BgLayout)
	p.takeDrop(root)
	if _, over := ui.DragOver[dragItem](root); over {
		root.InsetShadow(0, 0, 0, 2, tk.Primary)
	}
	root.Children(func() {
		ui.Row(c).Gap(6).Padding(6, 8).AlignItems(ui.Center).Background(tk.BgContainer).Children(func() {
			if action.ActionIcon(c, lucide.ArrowUp(), action.ActionIconProps{
				Size: theme.Small, Title: "上一级",
			}).Clicked() {
				p.Parent(changed)
			}
			ui.Text(c, p.Dir()).FontSize(12).Grow(1).SingleLine().TextColor(tk.TextSecondary)
			if action.ActionIcon(c, lucide.FolderPlus(), action.ActionIconProps{
				Size: theme.Small, Title: "新建目录",
			}).Clicked() {
				p.openDialog("mkdir", "新建目录")
			}
			if action.ActionIcon(c, lucide.RefreshCw(), action.ActionIconProps{
				Size: theme.Small, Title: "刷新",
			}).Clicked() {
				p.Refresh(changed)
			}
		})
		ui.Divider(c)
		if msg := p.Err(); msg != "" {
			ui.Row(c).Padding(6, 8).Children(func() {
				feedback.Alert(c, "读取失败", feedback.AlertProps{
					Type:        feedback.Error,
					Description: msg,
				})
			})
		}
		layout.ScrollArea(c, layout.ScrollAreaProps{Fade: true}, func() {
			ui.Column(c).FillWidth().Padding(4).Gap(2).Children(func() {
				for _, e := range p.Entries() {
					entryView(c, p, e, changed, tk)
				}
				// 空白处也收右键:新建目录、刷新。
				blank := ui.Row(c).FillWidth().Grow(1).MinHeight(60)
				switch overlay.ContextMenu(c, blank, overlay.DropdownMenuProps{Items: columnItems()}) {
				case "mkdir":
					p.openDialog("mkdir", "新建目录")
				case "refresh":
					p.Refresh(changed)
				}
			})
		}).Grow(1)
		if p.Loading() && len(p.Entries()) == 0 {
			ui.Row(c).FillWidth().Justify(ui.Center).Padding(12).Children(func() {
				feedback.Spin(c, feedback.SpinProps{Size: theme.Small}, nil)
			})
		}
		if sends := p.Sends(); len(sends) > 0 {
			ui.Column(c).Gap(4).Padding(6, 10).Background(tk.BgContainer).Children(func() {
				ui.Divider(c)
				for _, s := range sends {
					pct := float32(0)
					if s.total > 0 {
						pct = float32(s.done) / float32(s.total) * 100
					}
					status := feedback.ProgressActive
					label := s.name
					if s.err != "" {
						status = feedback.ProgressException
						label = s.name + ": " + s.err
					} else if s.total >= 0 && s.done >= s.total {
						status = feedback.ProgressSuccess
					} else if s.queued {
						label = s.name + " · 排队中"
					}
					feedback.Progress(c, pct, feedback.ProgressProps{
						Status:   status,
						ShowInfo: true,
						Label:    label,
						Format:   func(p float32) string { return fmt.Sprintf("%d%%", int(p+0.5)) },
					})
				}
			})
		}
		if p.dialogOpen() {
			p.dialogView(c, changed)
		}
	})
}

// entryView is one row of the listing: its icon, name and, for a file,
// its size and time, with a right-click menu of what the row can do.
func entryView(c *ui.Context, p *Panel, e sshclient.Entry, changed func(), tk *theme.Tokens) {
	row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Padding(5, 8).Radius(tk.BorderRadiusSM).
		Cursor(ui.CursorPointer).Transition(ui.ElementTransition{
		Duration: 120 * time.Millisecond, Ease: ui.CubicBezier(0.32, 0.72, 0, 1), Colors: true,
	})
	row.Drag(dragItem{from: p, name: e.Name, isDir: e.IsDir})
	p.takeDrop(row)
	if _, over := ui.DragOver[dragItem](row); over {
		row.Background(tk.FillTertiary)
	}
	switch overlay.ContextMenu(c, row, overlay.DropdownMenuProps{Items: rowItems(p, e)}) {
	case "enter":
		p.Visit(e.Name, changed)
	case "send":
		p.sendOut(e.Name, e.IsDir)
	case "edit":
		p.editOnline(e.Name)
	case "rename":
		p.openRename(e.Name)
	case "remove":
		p.Remove(e.Name, changed, plistSay)
	}
	// Hover 给一层浅底,按下再深一点;目录双击进入。
	switch {
	case row.Pressed():
		row.Background(tk.FillSecondary)
	case row.Hovered():
		row.Background(tk.FillTertiary)
	}
	row.Children(func() {
		glyph, color := lucide.File(), tk.TextTertiary
		if e.IsDir {
			glyph, color = lucide.Folder(), tk.Primary
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Grow(1).MinWidth(0).Children(func() {
			ui.Icon(c, glyph).FontSize(15).TextColor(color)
			ui.Text(c, e.Name).FontSize(13).Grow(1).SingleLine()
			if !e.IsDir {
				ui.Text(c, size(e.Size)).FontSize(11).TextColor(tk.TextTertiary)
				ui.Text(c, e.Moded.Format("01-02 15:04")).FontSize(11).TextColor(tk.TextTertiary)
			}
		})
	})
	if e.IsDir && row.DoubleClicked() {
		p.Visit(e.Name, changed)
	}
}

// rowItems is the context menu of one listing row: 进入 for a
// directory, 发送到对列 for a file, then 重命名 and 删除 for either;
// the remote column's files also offer 编辑 (在线编辑并回传).
func rowItems(p *Panel, e sshclient.Entry) []overlay.MenuItem {
	items := make([]overlay.MenuItem, 0, 5)
	if e.IsDir {
		items = append(items, overlay.MenuItem{Key: "enter", Label: "进入", Icon: lucide.FolderOpen()})
	} else {
		items = append(items, overlay.MenuItem{Key: "send", Label: "发送到对列", Icon: lucide.ArrowLeftRight()})
		if p.IsRemote {
			items = append(items, overlay.MenuItem{Key: "edit", Label: "编辑", Icon: lucide.PencilLine()})
		}
	}
	items = append(items,
		overlay.MenuItem{Key: "rename", Label: "重命名", Icon: lucide.Pencil()},
		overlay.MenuItem{Divider: true},
		overlay.MenuItem{Key: "remove", Label: "删除", Icon: lucide.Trash2(), Danger: true},
	)
	return items
}

// columnItems is the context menu of the column's blank space:
// 新建目录 asks a name in the small dialog, 刷新 reloads the listing.
func columnItems() []overlay.MenuItem {
	return []overlay.MenuItem{
		{Key: "mkdir", Label: "新建目录", Icon: lucide.FolderPlus()},
		{Key: "refresh", Label: "刷新", Icon: lucide.RefreshCw()},
	}
}

// dialogView is the 新建目录/重命名 dialog: a small modal with a name
// input and 确定/取消, over the column.
func (p *Panel) dialogView(c *ui.Context, changed func()) {
	kind := p.dialogKindName()
	title := map[string]string{"mkdir": "新建目录", "rename": "重命名"}[kind]
	ok := feedback.Modal(c, p.dialogFlag(), feedback.ModalProps{
		Title:      title,
		Width:      360,
		OkText:     "确定",
		CancelText: "取消",
	}, func() {
		field.Input(c, p.dialogBuffer(), field.InputProps{Placeholder: title})
	})
	if ok {
		p.closeDialogAct(kind, changed)
	}
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

// sendOut hands an entry of this column to the other side: files
// stream with progress, directories recurse. Single-column panels
// fall back to a save dialog for files.
func (p *Panel) sendOut(name string, isDir bool) {
	p.Lock()
	other := p.other
	dir := p.dir
	p.Unlock()
	if other == nil {
		if isDir {
			return // 单列面板不支持保存整目录
		}
		go func() {
			dest, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: "保存 " + name, DefaultPath: name})
			if err != nil || dest == "" {
				return
			}
			if err := p.fs.Send(join(dir, name), dest, LocalFS{}, nil); err != nil {
				plistSay("传输失败", err.Error())
			}
		}()
		return
	}
	other.takeIn(p, name, isDir)
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
