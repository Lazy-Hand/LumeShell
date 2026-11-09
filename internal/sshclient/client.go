// Package sshclient connects to SSH servers and wraps their sessions and
// SFTP for the app's terminal and file panel.
package sshclient

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Auth is one way to prove the user's identity to a server.
type Auth interface {
	methods() ([]ssh.AuthMethod, error)
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

func (a PasswordAuth) methods() ([]ssh.AuthMethod, error) {
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
	}, nil
}

func (a KeyAuth) methods() ([]ssh.AuthMethod, error) {
	key, err := loadKey(a.KeyPath, []byte(a.Passphrase))
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("key %s: %w", a.KeyPath, err)
	}
	return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
}

func (AgentAuth) methods() ([]ssh.AuthMethod, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, nil // 没有 agent 在跑,什么也不加
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("ssh-agent: %w", err)
	}
	signers, err := agent.NewClient(conn).Signers()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ssh-agent list: %w", err)
	}
	if len(signers) == 0 {
		conn.Close()
		return nil, nil
	}
	return []ssh.AuthMethod{ssh.PublicKeys(signers...)}, nil
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

// Config is where to connect and how.
type Config struct {
	Host    string
	Port    string // empty: 22
	User    string
	Auth    Auth
	Timeout time.Duration // empty: 10s
}

// Client is a connected SSH server. It yields its shell session to the
// terminal (Session) and its file access to the file panel (SFTP) over
// one connection.
type Client struct {
	ssh  *ssh.Client
	Conf Config
}

// Connect dials the server and authenticates the user.
func Connect(conf Config) (*Client, error) {
	if conf.Port == "" {
		conf.Port = "22"
	}
	if conf.Timeout == 0 {
		conf.Timeout = 10 * time.Second
	}
	var auths []ssh.AuthMethod
	if conf.Auth != nil {
		var err error
		if auths, err = conf.Auth.methods(); err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("no authentication configured")
	}
	// ssh-agent 的钥匙追加在最后:档案里配的认证先问,agent 兜底,
	// 让两种都通的服务器按档案的来。
	if extra, err := (AgentAuth{}).methods(); err == nil {
		auths = append(auths, extra...)
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(conf.Host, conf.Port), conf.Timeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s:%s: %w", conf.Host, conf.Port, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, net.JoinHostPort(conf.Host, conf.Port), &ssh.ClientConfig{
		User:            conf.User,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // TODO: known_hosts
		Timeout:         conf.Timeout,
	})
	if err != nil {
		conn.Close()
		if conf.Auth == nil {
			return nil, err
		}
		return nil, fmt.Errorf("connect %s@%s:%s: %w", conf.User, conf.Host, conf.Port, err)
	}
	return &Client{ssh: ssh.NewClient(sshConn, chans, reqs), Conf: conf}, nil
}

// Close ends the connection and everything on it.
func (c *Client) Close() error {
	if c == nil || c.ssh == nil {
		return nil
	}
	return c.ssh.Close()
}
