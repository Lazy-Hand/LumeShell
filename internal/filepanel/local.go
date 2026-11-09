package filepanel

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"lume/internal/sshclient"
)

// LocalFS is the panel's file channel over this machine's file system,
// so one panel type serves both columns of the browser.
type LocalFS struct{}

// ReadDir lists a directory of this machine.
func (LocalFS) ReadDir(dir string) ([]sshclient.Entry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]sshclient.Entry, 0, len(ents))
	for _, e := range ents {
		info, err := e.Info()
		if err != nil {
			continue // gone between listing and stat
		}
		out = append(out, sshclient.Entry{
			Name:  e.Name(),
			Size:  info.Size(),
			Mode:  info.Mode(),
			Moded: info.ModTime(),
			IsDir: e.IsDir(),
		})
	}
	sortEntries(out)
	return out, nil
}

func (LocalFS) Mkdir(dir string) error       { return os.Mkdir(dir, 0o755) }
func (LocalFS) Remove(p string) error        { return os.Remove(p) }

// Download copies a local file to a local path: the panel's download
// button over the local column stays but moves files, so beside itself
// it copies.
func (LocalFS) Download(src, dest string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0o644)
}

func (LocalFS) Upload(src, dest string) error      { return LocalFS{}.Download(src, dest) }
func (LocalFS) Close() error                       { return nil }

// LocalHome is where the local column starts: the user's home, with a
// fallback per platform when the system does not say.
func LocalHome() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home
	}
	switch runtime.GOOS {
	case "windows":
		return `C:\Users`
	default:
		return "/"
	}
}

// sortEntries orders a listing with directories first.
func sortEntries(es []sshclient.Entry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].IsDir != es[j].IsDir {
			return es[i].IsDir
		}
		return es[i].Name < es[j].Name
	})
}

// Base is the last part of a path, either file system's style.
func Base(p string) string { return filepath.Base(p) }
