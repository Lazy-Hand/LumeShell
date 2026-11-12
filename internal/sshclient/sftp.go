package sshclient

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pkg/sftp"
)

// Entry is one file or directory of the remote, as the panel shows it.
type Entry struct {
	Name    string
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
	IsDir   bool
}

// WriterFor is the writing half of a file channel: local and remote
// paths both open a stream this way, and Send's destination is one.
type WriterFor interface {
	Create(path string) (io.WriteCloser, error)
}

// SFTP is the connection's file access. It opens over the same SSH
// connection the terminal uses, so the user proves the server's identity
// once.
type SFTP struct {
	cl *sftp.Client
}

// SFTP opens the file channel of the connection. Failures end the
// connection, as a broken transport does.
func (c *Client) SFTP() (*SFTP, error) {
	cl, err := sftp.NewClient(c.ssh, sftp.UseConcurrentWrites(true))
	if err != nil {
		return nil, fmt.Errorf("sftp: %w", err)
	}
	return &SFTP{cl: cl}, nil
}

// Close ends the file channel, not the connection.
func (fs *SFTP) Close() error {
	if fs == nil || fs.cl == nil {
		return nil
	}
	return fs.cl.Close()
}

// ReadDir lists a directory; the panel sorts it, directories first.
func (fs *SFTP) ReadDir(path string) ([]Entry, error) {
	infos, err := fs.cl.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(infos))
	for _, in := range infos {
		out = append(out, Entry{
			Name:    in.Name(),
			Size:    in.Size(),
			Mode:    in.Mode(),
			ModTime: in.ModTime(),
			IsDir:   in.IsDir(),
		})
	}
	return out, nil
}

// Mkdir makes a directory, whose parents must exist, as the panel asks.
func (fs *SFTP) Mkdir(path string) error { return fs.cl.Mkdir(path) }

// Rename renames a file or a directory.
func (fs *SFTP) Rename(from, to string) error { return fs.cl.Rename(from, to) }

// Remove deletes one file; a directory must be empty, as rmdir is.
func (fs *SFTP) Remove(path string) error { return fs.cl.Remove(path) }

// Join joins a directory with a name, remote-style.
func (fs *SFTP) Join(dir, name string) string { return fs.cl.Join(dir, name) }

// Home is the user's home on the host, where a panel starts.
func (fs *SFTP) Home() (string, error) { return fs.cl.RealPath(".") }

// Send copies this channel's remote file at src into the destination
// channel's writer at remote, streaming so progress reports as bytes
// pass. A file past the chunk threshold goes in concurrent pieces when
// the destination takes writes at an offset (the local disk does, and a
// second SFTP channel would too).
func (fs *SFTP) Send(src, remote string, dst WriterFor, report func(done, total int64)) error {
	in, err := fs.cl.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a directory", src)
	}
	out, err := dst.Create(remote)
	if err != nil {
		return err
	}
	if st.Size() >= chunkThreshold {
		if wa, ok := out.(io.WriterAt); ok {
			err := SendChunked(in, wa, st.Size(), report)
			cerr := out.Close()
			if err != nil {
				return err
			}
			return cerr
		}
	}
	var from io.Reader = in
	if report != nil {
		from = &progressReader{r: in, total: st.Size(), report: report}
	}
	if _, err := io.Copy(out, from); err != nil {
		out.Close()
		return fmt.Errorf("send %s: %w", src, err)
	}
	return out.Close()
}

// Create opens the remote path for writing, as a Send destination.
func (fs *SFTP) Create(path string) (io.WriteCloser, error) { return fs.cl.Create(path) }

// progressReader reports how much of r has been read after each Read.
type progressReader struct {
	r      io.Reader
	done   int64
	total  int64
	report func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if n > 0 {
		p.report(p.done, p.total)
	}
	return n, err
}

// hand over the underlying client for the panel
