// snippets.go:命令片段——存的命令串,点一下写进终端,
// 省得长命令一遍遍敲。存在用户数据目录 snippets.json,不含密码,
// 不敏感。
package appui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"lumeshell/internal/atomicfile"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/theme"
)

// Snippet is one saved command: a name to pick it by, and the text that
// goes to the terminal when it's picked.
type Snippet struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Text string `json:"text"` // \n 会原样发给 shell,多行就是多条命令
}

// snippetStore is the app's one list, read on first use and shared by
// every terminal's drawer: a tab's add or delete lands in every tab and
// on disk, where a per-tab copy would let one drawer save its stale list
// over another's.
var snippetStore struct {
	mu     sync.Mutex
	list   []Snippet
	loaded bool
}

// snippets is a copy of the saved list.
func snippets() []Snippet {
	snippetStore.mu.Lock()
	defer snippetStore.mu.Unlock()
	if !snippetStore.loaded {
		snippetStore.list = loadSnippets()
		snippetStore.loaded = true
	}
	out := make([]Snippet, len(snippetStore.list))
	copy(out, snippetStore.list)
	return out
}

// editSnippets changes the list and saves it, under the one lock, so two
// tabs changing it at once both land.
func editSnippets(fn func([]Snippet) []Snippet) {
	snippetStore.mu.Lock()
	defer snippetStore.mu.Unlock()
	if !snippetStore.loaded {
		snippetStore.list = loadSnippets()
		snippetStore.loaded = true
	}
	snippetStore.list = fn(snippetStore.list)
	saveSnippets(snippetStore.list)
}

// SnippetView is the snippets drawer of one terminal: it lists what is
// saved, sends one on click, and takes a new one with name + text. Its
// fields are the UI thread's alone; the list it shows is snippetStore's.
type SnippetView struct {
	open    bool
	newName string
	newText string
	err     string
}

// NewSnippetView makes the drawer over the shared list.
func NewSnippetView() *SnippetView { return &SnippetView{} }

// Open flips the drawer's visibility.
func (v *SnippetView) Open() { v.open = !v.open }

// View builds the drawer when it is open, over the terminal page.
// term is where a clicked snippet's text goes.
func (v *SnippetView) View(c *ui.Context, term *terminal.Terminal) {
	if !v.open {
		return
	}
	list := snippets()
	tk := theme.TokensOf(c)

	feedback.Drawer(c, &v.open, feedback.DrawerProps{
		Placement: feedback.Right, Title: "命令片段", Size: 300,
	}, func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			if len(list) == 0 {
				ui.Text(c, "还没有片段,下面存一条").FontSize(12).TextColor(tk.TextTertiary)
			}
			for _, s := range list {
				s := s
				row := ui.Row(c).Key(s.ID).FillWidth().AlignItems(ui.Center).Gap(6).
					Padding(6, 8).Radius(tk.BorderRadiusSM).Cursor(ui.CursorPointer)
				if row.Hovered() {
					row.Background(tk.FillTertiary)
				}
				row.Children(func() {
					ui.Icon(c, lucide.Terminal()).FontSize(14).TextColor(tk.Primary)
					ui.Column(c).Grow(1).MinWidth(0).Children(func() {
						ui.Text(c, s.Name).FontSize(13).SingleLine()
						ui.Text(c, s.Text).FontSize(11).TextColor(tk.TextTertiary).SingleLine()
					})
					if action.ActionIcon(c, lucide.Trash2(), action.ActionIconProps{
						Title: "删除", Danger: true, Size: theme.Small,
					}).Clicked() {
						v.remove(s.ID)
					}
				})
				if row.Clicked() {
					v.send(term, s.Text)
				}
			}
			ui.Divider(c)
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "新片段").FontSize(12).Bold()
				name, text, err := v.newName, v.newText, v.err
				field.Input(c, &name, field.InputProps{Placeholder: "名字,如:看负载"})
				field.Input(c, &text, field.InputProps{Placeholder: "命令"})
				v.newName, v.newText = name, text
				if err != "" {
					ui.Text(c, err).FontSize(11).TextColor(tk.Error)
				}
				if action.Button(c, "保存片段", action.ButtonProps{
					Size: theme.Small,
				}).Clicked() {
					v.add()
				}
			})
		})
	})
}

// send writes the snippet's text to the terminal, plus a return when it
// doesn't end in one, so a one-line command runs on click.
func (v *SnippetView) send(term *terminal.Terminal, text string) {
	if text == "" || term == nil {
		return
	}
	if text[len(text)-1] != '\n' {
		text += "\r"
	}
	term.Send([]byte(text))
}

// add takes the form's name+text into the list and writes it to disk.
func (v *SnippetView) add() {
	name, text := v.newName, v.newText
	if name == "" || text == "" {
		v.err = "名字和命令都要填"
		return
	}
	s := Snippet{ID: newSnippetID(), Name: name, Text: text}
	editSnippets(func(list []Snippet) []Snippet { return append(list, s) })
	v.newName, v.newText, v.err = "", "", ""
}

// remove drops the snippet with id.
func (v *SnippetView) remove(id string) {
	editSnippets(func(list []Snippet) []Snippet {
		out := list[:0]
		for _, s := range list {
			if s.ID != id {
				out = append(out, s)
			}
		}
		return out
	})
}

// newSnippetID is a random id, for telling rows apart on delete.
func newSnippetID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// snippetsPath is where the list lives: beside profiles.json in the
// app's user-data dir.
func snippetsPath() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "snippets.json")
}

// loadSnippets reads the saved list; a missing or broken file is an
// empty list, not a failure.
func loadSnippets() []Snippet {
	path := snippetsPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []Snippet
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}

// saveSnippets writes the list; failures are quiet — the snippets are a
// convenience, not data worth a dialog.
func saveSnippets(list []Snippet) {
	path := snippetsPath()
	if path == "" {
		return
	}
	_ = atomicfile.WriteJSON(path, list)
}
