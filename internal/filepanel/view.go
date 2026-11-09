package filepanel

import (
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// base is the last part of a local path.
func base(local string) string { return filepath.Base(local) }

// View builds the panel: its toolbar, its listing and its errors. It
// reads `changed` out of wherever the panel lives, which redraws it with
// what the background goroutines bring in.
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
						if ui.Button(c, "⬇").Clicked() && !isDir {
							p.askDownload(name)
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
			if ui.Button(c, "上传文件…").Clicked() {
				p.askUpload()
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

// askDownload picks where the file goes locally, then starts the copy.
func (p *Panel) askDownload(name string) {
	go func() {
		local, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: "下载 " + name, DefaultPath: name})
		if err != nil || local == "" {
			return
		}
		p.Download(name, local, plistSay)
	}()
}

// askUpload picks files from the host machine, then copies them into
// the directory shown.
func (p *Panel) askUpload() {
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "上传文件", Multiple: true})
		if err != nil || len(paths) == 0 {
			return
		}
		for _, local := range paths {
			p.Upload(base(local), local, plistSay)
		}
	}()
}

// plistSay is how the panel reports transfer failures: mygo.Dialog
// runs its message on its own, from any goroutine.
func plistSay(title, body string) {
	mygo.Dialog.Message(mygo.MessageOptions{Type: mygo.MessageWarning, Title: title, Message: body})
}
