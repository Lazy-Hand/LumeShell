package filepanel

import (
	"io"
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
func (LocalFS) Rename(from, to string) error { return os.Rename(from, to) }

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

// Send copies this channel's local file at src to the destination
// channel's remote path: the source reads from disk, the destination
// writes (local file or SFTP), and report hears the bytes as they go.
// A file past the chunk threshold goes in concurrent pieces when the
// destination takes writes at an offset — an upload to the server does.
func (LocalFS) Send(src, remote string, dst sshclient.WriterFor, report func(done, total int64)) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := dst.Create(remote)
	if err != nil {
		return err
	}
	if st.Size() >= 8<<20 {
		if wa, ok := out.(io.WriterAt); ok {
			err := sshclient.SendChunked(in, wa, st.Size(), report)
			cerr := out.Close()
			if err != nil {
				return err
			}
			return cerr
		}
	}
	var from io.Reader = in
	if report != nil {
		from = &reportReader{r: in, total: st.Size(), report: report}
	}
	if _, err := io.Copy(out, from); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Create opens the local path for writing, as a Send destination.
func (LocalFS) Create(path string) (io.WriteCloser, error) { return os.Create(path) }

// reportReader reports how much of r has been read after each Read.
type reportReader struct {
	r      io.Reader
	done   int64
	total  int64
	report func(done, total int64)
}

func (p *reportReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if n > 0 {
		p.report(p.done, p.total)
	}
	return n, err
}

func (LocalFS) Close() error { return nil }

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
