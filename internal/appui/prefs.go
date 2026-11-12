// prefs.go:界面偏好——目前就一项:主题跟随系统、强制浅色、
// 强制深色。存 prefs.json,启动时先读它再建窗口。
package appui

import (
	"encoding/json"
	"os"
	"path/filepath"

	"lumeshell/internal/atomicfile"

	"github.com/egoist/mygo"
)

// prefFile is what prefs.json holds.
type prefFile struct {
	Theme string `json:"theme,omitempty"` // "system"|"light"|"dark"; "" = system
}

func prefsPath() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "prefs.json")
}

// LoadThemeSource reads the saved appearance; nothing saved or an
// unknown value is the system default.
func LoadThemeSource() mygo.ThemeSource {
	path := prefsPath()
	if path == "" {
		return mygo.ThemeSystem
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return mygo.ThemeSystem
	}
	var f prefFile
	if json.Unmarshal(data, &f) != nil {
		return mygo.ThemeSystem
	}
	switch mygo.ThemeSource(f.Theme) {
	case mygo.ThemeLight:
		return mygo.ThemeLight
	case mygo.ThemeDark:
		return mygo.ThemeDark
	default:
		return mygo.ThemeSystem
	}
}

// SaveThemeSource persists the choice; it also applies it, so a click
// flips the look at once.
func SaveThemeSource(s mygo.ThemeSource) {
	mygo.Theme.SetSource(s)
	path := prefsPath()
	if path == "" {
		return
	}
	_ = atomicfile.WriteJSON(path, prefFile{Theme: string(s)})
}
