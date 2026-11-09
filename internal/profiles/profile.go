// Package profiles stores the hosts the user has connected to, so the
// list page can show them again. Secrets never reach disk: only where to
// connect and how to prove the user's identity are saved.
package profiles

import "time"

// AuthType says how a profile connects, for the form to ask for the
// right secret again, not for the secret itself.
type AuthType string

// The ways to connect.
const (
	// Password matches PasswordAuth.
	Password AuthType = "password"
	// Key matches KeyAuth, with or without a passphrase.
	Key AuthType = "key"
)

// Profile is one saved host.
type Profile struct {
	ID        string    `json:"id"`
	Label     string    `json:"label,omitempty"`
	Host      string    `json:"host"`
	Port      string    `json:"port,omitempty"` // empty: 22
	User      string    `json:"user"`
	Auth      AuthType  `json:"auth"`
	KeyPath   string    `json:"keyPath,omitempty"` // Auth is Key
	HasPass   bool      `json:"hasPass"`           // the key had a passphrase or the host a password
	LastUsed  time.Time `json:"lastUsed"`
	CreatedAt time.Time `json:"createdAt"`
}

// Title is what the card and the terminal title show.
func (p Profile) Title() string {
	if p.Label != "" {
		return p.Label
	}
	return p.Host
}

// Endpoint is where the profile connects, for Confirm and display.
func (p Profile) Endpoint() string {
	port := p.Port
	if port == "" {
		port = "22"
	}
	return p.User + "@" + p.Host + ":" + port
}
