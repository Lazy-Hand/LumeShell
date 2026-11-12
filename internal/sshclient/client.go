// Package sshclient connects to SSH servers and wraps their sessions and
// SFTP for the app's terminal and file panel.
package sshclient

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Auth is one way to prove the user's identity to a server. methods may
// hold a resource open for the connection, as the ssh-agent's socket,
// and returns it as a closer for the Client to close with the connection.
type Auth interface {
	methods() (auths []ssh.AuthMethod, closer io.Closer, err error)
}

// PasswordAuth proves identity with the user's password. It also answers
// keyboard-interactive challenges with the same password, for servers
// that only take those.
type PasswordAuth struct {
	Password string
}

// KeyAuth proves identity with a private key file, whose passphrase may
// be empty.
type KeyAuth struct {
	KeyPath    string
	Passphrase string
}

// AgentAuth proves identity with the keys in the system's ssh-agent;
// its methods come after whatever Auth is already on the profile, so a
// saved password or key still wins when the server takes either.
type AgentAuth struct{}

func (a PasswordAuth) methods() ([]ssh.AuthMethod, io.Closer, error) {
	return []ssh.AuthMethod{
		ssh.Password(a.Password),
		ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
			// 服务器问什么我们都回密码——常规 keyboard-interactive
			// 只问一次"Password:";真要交互的多问题场景答不了,
			// 让 auth 失败就行。
			out := make([]string, len(questions))
			for i := range out {
				out[i] = a.Password
			}
			return out, nil
		}),
	}, nil, nil
}

func (a KeyAuth) methods() ([]ssh.AuthMethod, io.Closer, error) {
	key, err := loadKey(a.KeyPath, []byte(a.Passphrase))
	if err != nil {
		return nil, nil, err
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("key %s: %w", a.KeyPath, err)
	}
	return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil, nil
}

func (AgentAuth) methods() ([]ssh.AuthMethod, io.Closer, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil, nil // 没有 agent 在跑,什么也不加
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, fmt.Errorf("ssh-agent: %w", err)
	}
	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("ssh-agent list: %w", err)
	}
	if len(signers) == 0 {
		conn.Close()
		return nil, nil, nil
	}
	// The signers sign through conn while the connection authenticates,
	// so it stays open until the Client that used it closes.
	return []ssh.AuthMethod{ssh.PublicKeys(signers...)}, conn, nil
}

// loadKey reads a PEM private key file, decrypting it with passphrase
// when the key has one.
func loadKey(path string, passphrase []byte) (any, error) {
	data, err := osReadFile(path)
	if err != nil {
		return nil, err
	}
	raw, err := ssh.ParseRawPrivateKey(data)
	if err == nil {
		return raw, nil
	}
	// A key with its cipher the only reason parsing fails gets decrypted.
	var missing *ssh.PassphraseMissingError
	if !errors.As(err, &missing) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(passphrase) == 0 {
		return nil, fmt.Errorf("key %s is encrypted; enter its passphrase", path)
	}
	raw, err = ssh.ParseRawPrivateKeyWithPassphrase(data, passphrase)
	if err != nil {
		return nil, fmt.Errorf("decrypt %s: %w", path, err)
	}
	return raw, nil
}

// Jump is an optional bastion: Connect reaches it first, then dials
// the target through it, as ssh -J does.
type Jump struct {
	Host, Port, User string
	Auth             Auth // the same kinds as Config.Auth
}

// Config is where to connect and how.
type Config struct {
	Host    string
	Port    string // empty: 22
	User    string
	Auth    Auth
	Jump    *Jump         // empty: dial the target directly
	Timeout time.Duration // empty: 10s

	// HostKey verifies the server's key before the connection carries
	// anything. Empty checks the user's known_hosts and trusts no new
	// host, which is what Connect alone can do; the app passes one that
	// offers a new key to the user.
	HostKey ssh.HostKeyCallback
}

// Client is a connected SSH server. It yields its shell session to the
// terminal (Session) and its file access to the file panel (SFTP) over
// one connection; when a jump host carried the dial, closer keeps its
// SSH client too, so Close ends both.
type Client struct {
	ssh      *ssh.Client
	closer   io.Closer
	fwds     *tunnels
	fwdsOnce sync.Once
	auth     []io.Closer // sockets the auth methods signed through
}

// hostKey is the callback the handshake uses: Config's own, or the
// known_hosts check that trusts nothing new.
func (c Config) hostKey() ssh.HostKeyCallback {
	if c.HostKey != nil {
		return c.HostKey
	}
	return HostKeys(nil)
}

// authMethods is the auth chain for a Config: the profile's own ways
// first, the ssh-agent after. The closers hold what the methods need
// while they authenticate, and are closed with the connection.
func (c Config) authMethods() ([]ssh.AuthMethod, []io.Closer, error) {
	if c.Auth == nil {
		return nil, nil, errors.New("no authentication configured")
	}
	auths, closer, err := c.Auth.methods()
	if err != nil {
		return nil, nil, err
	}
	var closers []io.Closer
	if closer != nil {
		closers = append(closers, closer)
	}
	if extra, ec, err := (AgentAuth{}).methods(); err == nil {
		auths = append(auths, extra...)
		if ec != nil {
			closers = append(closers, ec)
		}
	}
	return auths, closers, nil
}

// closeAll closes every closer, ignoring their errors.
func closeAll(closers []io.Closer) {
	for _, c := range closers {
		if c != nil {
			c.Close()
		}
	}
}

// dial returns a live transport to conf's SSH port: direct TCP, or a
// channel through the jump host's SSH connection.
func (c Config) dial() (net.Conn, io.Closer, error) {
	if c.Jump == nil {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.Host, c.Port), c.Timeout)
		if err != nil {
			return nil, nil, err
		}
		return conn, conn, nil
	}
	jumpConf := Config{
		Host:    c.Jump.Host,
		Port:    c.Jump.Port,
		User:    c.Jump.User,
		Auth:    c.Jump.Auth,
		Timeout: c.Timeout,
		HostKey: c.HostKey,
	}
	auths, authClosers, err := jumpConf.authMethods()
	if err != nil {
		return nil, nil, fmt.Errorf("jump auth: %w", err)
	}
	jc, err := net.DialTimeout("tcp", net.JoinHostPort(jumpConf.Host, jumpConf.Port), c.Timeout)
	if err != nil {
		closeAll(authClosers)
		return nil, nil, fmt.Errorf("dial jump %s:%s: %w", jumpConf.Host, jumpConf.Port, err)
	}
	jConn, jChans, jReqs, err := ssh.NewClientConn(jc, net.JoinHostPort(jumpConf.Host, jumpConf.Port), &ssh.ClientConfig{
		User:            jumpConf.User,
		Auth:            auths,
		HostKeyCallback: jumpConf.hostKey(),
		Timeout:         c.Timeout,
	})
	if err != nil {
		jc.Close()
		closeAll(authClosers)
		return nil, nil, fmt.Errorf("connect jump %s@%s:%s: %w", jumpConf.User, jumpConf.Host, jumpConf.Port, err)
	}
	jump := ssh.NewClient(jConn, jChans, jReqs)
	conn, err := jump.Dial("tcp", net.JoinHostPort(c.Host, c.Port))
	if err != nil {
		jump.Close()
		closeAll(authClosers)
		return nil, nil, fmt.Errorf("dial %s:%s via jump: %w", c.Host, c.Port, err)
	}
	return conn, &jumpCloser{Conn: conn, jump: jump, closers: authClosers}, nil
}

// jumpCloser closes the inner channel, the jump's client, and what the
// jump's auth held open.
type jumpCloser struct {
	net.Conn
	jump    *ssh.Client
	closers []io.Closer
}

func (j *jumpCloser) Close() error {
	err := j.Conn.Close()
	j.jump.Close()
	closeAll(j.closers)
	return err
}

// Connect dials the server and authenticates the user.
func Connect(conf Config) (*Client, error) {
	if conf.Port == "" {
		conf.Port = "22"
	}
	if conf.Timeout == 0 {
		conf.Timeout = 10 * time.Second
	}
	auths, authClosers, err := conf.authMethods()
	if err != nil {
		return nil, err
	}
	conn, closer, err := conf.dial()
	if err != nil {
		closeAll(authClosers)
		return nil, err
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, net.JoinHostPort(conf.Host, conf.Port), &ssh.ClientConfig{
		User:            conf.User,
		Auth:            auths,
		HostKeyCallback: conf.hostKey(),
		Timeout:         conf.Timeout,
	})
	if err != nil {
		closer.Close()
		closeAll(authClosers)
		return nil, fmt.Errorf("connect %s@%s:%s: %w", conf.User, conf.Host, conf.Port, err)
	}
	client := &Client{ssh: ssh.NewClient(sshConn, chans, reqs), closer: closer, fwds: &tunnels{}, auth: authClosers}
	go client.keepalive(30 * time.Second)
	return client, nil
}

// keepalive pings the server on an interval so idle connections stay
// up through NATs and firewalls. It stops when the connection does.
func (c *Client) keepalive(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for range t.C {
		if _, _, err := c.ssh.SendRequest("keepalive@lumeshell", true, nil); err != nil {
			return // 连接死了,退出循环
		}
	}
}

// Close ends the connection and everything on it: the jump host's too,
// and the sockets its auth signed through.
func (c *Client) Close() error {
	if c == nil || c.ssh == nil {
		return nil
	}
	c.CloseTunnels()
	err := c.ssh.Close()
	if c.closer != nil {
		c.closer.Close()
	}
	closeAll(c.auth)
	c.auth = nil
	return err
}
