package sshclient

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// Entry is one file or directory of the remote, as the panel shows it.
type Entry struct {
	Name  string
	Size  int64
	Mode  os.FileMode
	Moded time.Time
	IsDir bool
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
	cl, err := sftp.NewClient(c.ssh)
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

// ReadDir lists a directory, sorted with directories first.
func (fs *SFTP) ReadDir(path string) ([]Entry, error) {
	infos, err := fs.cl.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(infos))
	for _, in := range infos {
		out = append(out, Entry{
			Name:  in.Name(),
			Size:  in.Size(),
			Mode:  in.Mode(),
			Moded: in.ModTime(),
			IsDir: in.IsDir(),
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

// Download copies a remote file to the local path dest.
func (fs *SFTP) Download(remote, dest string) error {
	if err := fs.get(remote, dest); err != nil {
		return err
	}
	_ = os.Chmod(dest, 0o700)
	return nil
}

func (fs *SFTP) get(remote, dest string) error {
	in, err := fs.cl.Open(remote)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a directory", remote)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s: %w", remote, err)
	}
	return out.Close()
}

// Upload copies a local file to the remote path.
func (fs *SFTP) Upload(local, remote string) error {
	in, err := os.Open(local)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := fs.cl.Create(remote)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("upload %s: %w", local, err)
	}
	return out.Close()
}

// hand over the underlying client for the panel
var _ = ssh.TerminalModes{}
