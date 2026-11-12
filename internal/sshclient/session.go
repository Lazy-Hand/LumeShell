package sshclient

import (
	"fmt"
	"io"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Session is one PTY over the connection. It is what the terminal view
// reads and writes: what the user types goes through Write, what the
// remote program shows comes out of Read, and Resize follows the view.
type Session struct {
	sess *ssh.Session
	// stdin takes what Write writes; stdout is what Read reads.
	stdin  io.WriteCloser
	stdout io.Reader
	// eof closes when the session has ended, so Read can report its end.
	eof chan struct{}
	// mu guards end, the error of the end, which Read and main both read.
	mu  sync.Mutex
	end error
}

// terminalTerm is the terminal type the remote sees, one libghostty-vt
// speaks fully.
const terminalTerm = "xterm-256color"

// Shell opens a PTY of cols×rows cells and runs the user's login shell
// on it.
func (c *Client) Shell(cols, rows int) (*Session, error) {
	sess, err := c.ssh.NewSession()
	if err != nil {
		return nil, err
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty(terminalTerm, rows, cols, modes); err != nil {
		sess.Close()
		return nil, fmt.Errorf("pty: %w", err)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	// Over a PTY the system merges stderr into the terminal, so stdout
	// alone is read here.
	s := &Session{sess: sess, stdin: stdin, stdout: stdout, eof: make(chan struct{})}
	if err := s.start(); err != nil {
		sess.Close()
		return nil, err
	}
	return s, nil
}

// start runs the shell and watches for its end.
func (s *Session) start() error {
	// Shell asks the server for the user's default login shell, as
	// terminal apps do; no command of ours runs.
	if err := s.sess.Shell(); err != nil {
		return err
	}
	go func() {
		err := s.sess.Wait()
		s.mu.Lock()
		s.end = err
		s.mu.Unlock()
		close(s.eof)
	}()
	return nil
}

// Read reads what the remote program shows, to the terminal view.
func (s *Session) Read(p []byte) (int, error) {
	n, err := s.stdout.Read(p)
	if err != nil && n == 0 {
		// The reader ends when the session has ended; a read that fails
		// while the shell is still running waits for it, as closing the
		// window ends it.
		<-s.eof
		if e := s.EndErr(); e != nil {
			return 0, e
		}
		return 0, io.EOF
	}
	return n, err
}

// EndErr is why the session ended, once it has: nil for a shell that
// exited cleanly, an *ssh.ExitError for a program that exited wrong, and
// the transport's error for a connection that dropped.
func (s *Session) EndErr() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.end
}

// Write writes what the user typed to the remote program.
func (s *Session) Write(p []byte) (int, error) { return s.stdin.Write(p) }

// Close ends the session: the shell stops and the view shows its end.
func (s *Session) Close() error {
	if s == nil || s.sess == nil {
		return nil
	}
	s.stdin.Close()
	s.sess.Close()
	return nil
}

// Resize tells the remote program its window is now cols×rows cells.
func (s *Session) Resize(cols, rows int) error {
	if s == nil || s.sess == nil {
		return nil
	}
	_ = s.sess.WindowChange(rows, cols)
	return nil
}
