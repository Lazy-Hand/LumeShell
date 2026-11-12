// logging.go:会话日志——把终端读到的输出原样写进一个文件。
// 原始字节(含转义序列),能 replay;存 logs/ 下按会话起名,新的
// 会话写新文件,某个文件长到上限就换下一个。
package sshclient

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxLogBytes is the size a log reaches before the session starts a new
// one beside it, so a long-running or chatty session cannot fill the disk.
const maxLogBytes = 16 << 20

// SessionLogger writes every byte Read from a session to a file, for
// replay or audit. Open one per session and Close it when the session
// ends; a nil *SessionLogger is a valid no-op, so callers need no nil
// checks.
type SessionLogger struct {
	mu   sync.Mutex
	f    *os.File // nil: logging off
	bw   *bufio.Writer
	dir  string // where the next part goes
	base string // the name every part starts with
	part int    // 1 for the first file, 2 for the next, ...
	n    int64  // bytes in the current file
	path string // the current file
}

// OpenSessionLogger starts a log at a new file in dir named after name
// and the time it opened, so sessions from different runs never mix. A
// dir of "" or an unwritable path gives a logger that records nothing.
func OpenSessionLogger(dir, name string) *SessionLogger {
	if dir == "" {
		return &SessionLogger{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &SessionLogger{}
	}
	l := &SessionLogger{
		dir:  dir,
		base: name + "-" + time.Now().Format("20060102-150405"),
	}
	if !l.openPart() {
		return &SessionLogger{}
	}
	l.writeRaw([]byte(fmt.Sprintf("\n\x1b[35m── session log %s ──\x1b[0m\n",
		time.Now().Format("2006-01-02 15:04:05"))))
	return l
}

// openPart opens the next file of the log, numbered from 1. It reports
// whether the file is open.
func (l *SessionLogger) openPart() bool {
	for l.part < 1000 {
		l.part++
		path := filepath.Join(l.dir, l.base+".log")
		if l.part > 1 {
			path = filepath.Join(l.dir, fmt.Sprintf("%s-%d.log", l.base, l.part))
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			if os.IsExist(err) {
				continue // taken, as by a session that opened in the same second
			}
			return false
		}
		l.f, l.bw, l.path, l.n = f, bufio.NewWriterSize(f, 64<<10), path, 0
		return true
	}
	return false
}

// Write records a chunk of terminal output. It satisfies io.Writer so a
// scanning Conn can fan its Read out to both the terminal and the log.
func (l *SessionLogger) Write(p []byte) (int, error) {
	l.writeRaw(p)
	return len(p), nil
}

func (l *SessionLogger) writeRaw(p []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.bw == nil {
		return
	}
	// A part rotates before the bytes that would pass the cap, so every
	// file it opens holds output and none is left empty at the boundary.
	if l.n > 0 && l.n+int64(len(p)) > maxLogBytes && !l.rotateLocked() {
		l.closeLocked()
		return
	}
	n, err := l.bw.Write(p)
	l.n += int64(n)
	if err != nil {
		l.closeLocked()
	}
}

// rotateLocked ends the current file and starts the next part; the log
// stops when a part cannot be opened. l.mu is held.
func (l *SessionLogger) rotateLocked() bool {
	l.bw.Flush()
	l.f.Close()
	l.f, l.bw = nil, nil
	return l.openPart()
}

// closeLocked flushes and closes the current file. l.mu is held.
func (l *SessionLogger) closeLocked() {
	if l.f == nil {
		return
	}
	if l.bw != nil {
		l.bw.Flush()
	}
	l.f.Close()
	l.f, l.bw = nil, nil
}

// Path is the file the log writes, "" when logging is off.
func (l *SessionLogger) Path() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return ""
	}
	return l.path
}

// Close ends the log.
func (l *SessionLogger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeLocked()
}
