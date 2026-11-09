// logging.go:会话日志——把终端读到的输出原样写进一个文件。
// 原始字节(含转义序列),能 replay;存 logs/ 下按会话起名。
package sshclient

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionLogger writes every byte Read from a session to a file, for
// replay or audit. Open one per session and Close it when the session
// ends; a nil *SessionLogger is a valid no-op, so callers need no nil
// checks.
type SessionLogger struct {
	mu sync.Mutex
	f  *os.File // nil: logging off
}

// OpenSessionLogger starts a log at dir/name.log; dir "" or an
// unwritable path gives a logger that records nothing.
func OpenSessionLogger(dir, name string) *SessionLogger {
	if dir == "" {
		return &SessionLogger{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &SessionLogger{}
	}
	f, err := os.OpenFile(filepath.Join(dir, name+".log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return &SessionLogger{}
	}
	l := &SessionLogger{f: f}
	l.writeRaw([]byte(fmt.Sprintf("\n\x1b[35m── session log %s ──\x1b[0m\n",
		time.Now().Format("2006-01-02 15:04:05"))))
	return l
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
	if l.f != nil {
		l.f.Write(p)
	}
}

// Path is the file the log writes, "" when logging is off.
func (l *SessionLogger) Path() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return ""
	}
	return l.f.Name()
}

// Close ends the log.
func (l *SessionLogger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}
