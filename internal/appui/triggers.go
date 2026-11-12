// triggers.go:关键字触发器——终端输出扫到用户存的正则就提醒:
// 窗口闪烁一下,tab 上挂个红点,点开页面消掉。模式存
// triggers.json,跟片段一样不含敏感信息。
package appui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"

	"lumeshell/internal/atomicfile"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/lobehub/lobe-mygo/pkg/action"
	"github.com/lobehub/lobe-mygo/pkg/feedback"
	"github.com/lobehub/lobe-mygo/pkg/field"
	"github.com/lobehub/lobe-mygo/pkg/lucide"
	"github.com/lobehub/lobe-mygo/pkg/theme"
)

// Trigger is one pattern to watch the terminal's output for.
type Trigger struct {
	ID      string `json:"id"`
	Pattern string `json:"pattern"` // Go 正则;写坏的存了也不编
}

// triggersPath is beside snippets.json in the app's user-data dir.
func triggersPath() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "triggers.json")
}

// loadTriggers reads the saved patterns; a missing or broken file is
// an empty list.
func loadTriggers() []Trigger {
	path := triggersPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []Trigger
	if json.Unmarshal(data, &out) != nil {
		return nil
	}
	return out
}

// saveTriggers writes the list, quietly.
func saveTriggers(list []Trigger) {
	path := triggersPath()
	if path == "" {
		return
	}
	_ = atomicfile.WriteJSON(path, list)
}

// triggerStore is the app's one list, shared by every session, and
// gen counts its changes so each session's watcher picks up what
// another session's drawer saved.
var triggerStore struct {
	mu     sync.Mutex
	list   []Trigger
	loaded bool
	gen    atomic.Int64
}

// triggers is a copy of the saved list and the generation it is.
func triggers() ([]Trigger, int64) {
	triggerStore.mu.Lock()
	defer triggerStore.mu.Unlock()
	if !triggerStore.loaded {
		triggerStore.list = loadTriggers()
		triggerStore.loaded = true
	}
	out := make([]Trigger, len(triggerStore.list))
	copy(out, triggerStore.list)
	return out, triggerStore.gen.Load()
}

// editTriggers changes the list and saves it under the one lock, then
// bumps the generation so live watchers re-arm.
func editTriggers(fn func([]Trigger) []Trigger) {
	triggerStore.mu.Lock()
	if !triggerStore.loaded {
		triggerStore.list = loadTriggers()
		triggerStore.loaded = true
	}
	triggerStore.list = fn(triggerStore.list)
	saveTriggers(triggerStore.list)
	triggerStore.gen.Add(1)
	triggerStore.mu.Unlock()
}

// TriggerWatcher wraps a session's output stream: every chunk the
// terminal reads passes through Scan, and a hit calls fire (which main
// wires to flashing the window and dotting the tab). Compiled lazily
// from the saved list and rebuilt when that list changes.
type TriggerWatcher struct {
	mu    sync.Mutex
	list  []Trigger
	res   []*regexp.Regexp // list 编译来的,同序
	gen   int64            // the triggerStore generation res came from
	fired bool             // 本连接周期内报过一次就不重复闪
	fire  func(matched string)
}

// NewTriggerWatcher loads the list; fire runs on a reader goroutine,
// never the UI thread.
func NewTriggerWatcher(fire func(string)) *TriggerWatcher {
	w := &TriggerWatcher{fire: fire}
	w.Reload()
	return w
}

// Reload re-reads the saved patterns, after a drawer changed them.
func (w *TriggerWatcher) Reload() {
	list, gen := triggers()
	res := make([]*regexp.Regexp, 0, len(list))
	for _, t := range list {
		if re, err := regexp.Compile(t.Pattern); err == nil {
			res = append(res, re)
		}
	}
	w.mu.Lock()
	w.list, w.res, w.gen = list, res, gen
	w.mu.Unlock()
}

// Seen reports whether a trigger has fired since the tab was looked
// at; Clear resets it, which showing the session does.
func (w *TriggerWatcher) Seen() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fired
}

// Clear drops the fired mark.
func (w *TriggerWatcher) Clear() {
	w.mu.Lock()
	w.fired = false
	w.mu.Unlock()
}

// Scan looks for a pattern in the output chunk and fires on the first
// hit of the session, at most once per Clear. It picks up a list another
// drawer saved before it looks.
func (w *TriggerWatcher) Scan(chunk []byte) {
	w.mu.Lock()
	if w.gen != triggerStore.gen.Load() {
		w.mu.Unlock()
		w.Reload()
		w.mu.Lock()
	}
	if w.fired {
		w.mu.Unlock()
		return
	}
	for _, re := range w.res {
		if loc := re.FindIndex(chunk); loc != nil {
			w.fired = true
			matched := string(bytes.TrimSpace(chunk[loc[0]:loc[1]]))
			if w.fire != nil {
				go w.fire(matched) // 别在终端读循环里干重活
			}
			w.mu.Unlock()
			return
		}
	}
	w.mu.Unlock()
}

// HasPatterns reports whether the watcher has anything to scan for —
// the session skips wrapping its Conn when it has none.
func (w *TriggerWatcher) HasPatterns() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.res) > 0
}

// TriggerView is the drawer managing the pattern list, on the terminal
// page next to snippets. Its fields are the UI thread's alone; the list
// it shows is triggerStore's.
type TriggerView struct {
	watcher *TriggerWatcher
	open    bool
	newPat  string
	err     string
}

// NewTriggerView builds the drawer over the session's watcher.
func NewTriggerView(w *TriggerWatcher) *TriggerView {
	return &TriggerView{watcher: w}
}

// Open flips the drawer.
func (v *TriggerView) Open() { v.open = !v.open }

// View builds the drawer when open.
func (v *TriggerView) View(c *ui.Context) {
	if !v.open {
		return
	}
	list, _ := triggers()
	tk := theme.TokensOf(c)

	feedback.Drawer(c, &v.open, feedback.DrawerProps{
		Placement: feedback.Right, Title: "触发器", Size: 300,
	}, func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, "终端输出匹配到正则时,窗口闪烁并在 tab 上标红点").FontSize(11).TextColor(tk.TextTertiary)
			if len(list) == 0 {
				ui.Text(c, "还没有模式,下面加一条").FontSize(12).TextColor(tk.TextTertiary)
			}
			for _, tr := range list {
				tr := tr
				row := ui.Row(c).Key(tr.ID).FillWidth().AlignItems(ui.Center).Gap(6).
					Padding(6, 8).Radius(tk.BorderRadiusSM)
				if row.Hovered() {
					row.Background(tk.FillTertiary)
				}
				row.Children(func() {
					ui.Icon(c, lucide.BellRing()).FontSize(14).TextColor(tk.Primary)
					ui.Text(c, tr.Pattern).FontSize(12).Grow(1).MinWidth(0).SingleLine()
					if action.ActionIcon(c, lucide.Trash2(), action.ActionIconProps{
						Title: "删除", Danger: true, Size: theme.Small,
					}).Clicked() {
						v.remove(tr.ID)
					}
				})
			}
			ui.Divider(c)
			ui.Column(c).Gap(6).Children(func() {
				ui.Text(c, "新模式").FontSize(12).Bold()
				pat, err := v.newPat, v.err
				field.Input(c, &pat, field.InputProps{Placeholder: `正则,如 (?i)error|failed`})
				v.newPat = pat
				if err != "" {
					ui.Text(c, err).FontSize(11).TextColor(tk.Error)
				}
				if action.Button(c, "添加", action.ButtonProps{Size: theme.Small}).Clicked() {
					v.add()
				}
			})
		})
	})
}

// add compiles the pattern — a bad regex never leaves the form — then
// puts it on the list and re-arms the watchers.
func (v *TriggerView) add() {
	pat := v.newPat
	if _, err := regexp.Compile(pat); pat == "" || err != nil {
		if pat == "" {
			v.err = "模式不能为空"
		} else {
			v.err = "正则写错了: " + err.Error()
		}
		return
	}
	tr := Trigger{ID: newSnippetID(), Pattern: pat}
	editTriggers(func(list []Trigger) []Trigger { return append(list, tr) })
	v.newPat, v.err = "", ""
	if v.watcher != nil {
		v.watcher.Reload()
	}
}

// remove drops the pattern with id and re-arms the watchers.
func (v *TriggerView) remove(id string) {
	editTriggers(func(list []Trigger) []Trigger {
		out := list[:0]
		for _, tr := range list {
			if tr.ID != id {
				out = append(out, tr)
			}
		}
		return out
	})
	if v.watcher != nil {
		v.watcher.Reload()
	}
}
