# LumeShell

An SSH client built on [mygo](https://github.com/egoist/mygo)'s native UI — no webview, no Electron. macOS, one window, tabs along the top.

## What it does

- **Host list** of saved connections: label, host, port, user, auth (password or private key), optional jump host. Cards are edited or deleted in place; new connections can be imported from `~/.ssh/config`.
- **Terminal tabs**: each connection opens a tab with a full terminal. Several sessions stay open at once; "+" on the tab bar goes back to the list to connect another. A dropped connection reconnects once on its own before the tab reports its end.
- **Local shell**: a tab running your login shell on this machine — no SSH, no SFTP.
- **File browser**: a two-column SFTP page over the same connection — remote files beside local ones, transfer between panes, view and edit remote files. It rides the session's SSH connection, so no second login.
- **Port forwards**: per-session tunnels, listed and added from the terminal page; they live as long as the session does.
- **Snippets**: saved command strings per terminal — click one to write it into the terminal. Stored in `snippets.json` in the user data directory, nothing sensitive.
- **Triggers**: keyword triggers — terminal output matching a saved regex flashes the window and marks the tab with a red dot, cleared when you look at it. Patterns in `triggers.json`.
- **Session logs**: terminal output is logged to `logs/` beside the profiles file, one file per session named after the host.
- **Appearance**: 跟随系统 / 浅色 / 深色, kept in `prefs.json`.
- **Auto-update**: signed updates over GitHub Releases — "检查更新…" in the app menu checks now, a background check runs daily. Updates install in place and relaunch.
- Secrets (passwords, key passphrases) never reach disk — kept in memory for reconnect during the session only.

## Layout

```
main.go                window, tabs, sessions: connect, reconnect, file page, teardown
internal/appui/        pages: host list, connection form, terminal page, files page,
                       tunnels, snippets, triggers, prefs, theme
internal/profiles/     saved hosts on disk (no secrets); ~/.ssh/config import
internal/sshclient/    SSH connect, shell session, SFTP, port forwards, session log
internal/filepanel/    the two-column file panel (remote SFTP side + local side)
resources/             app icon
```

## Run

```sh
bun install
bun run dev     # mygo dev (native window)
bun run build   # packaged .app + .dmg in build/
```

## Release

Push a `v*` tag: `.github/workflows/release.yml` builds a universal DMG on
macOS, signs the update with `MYGO_UPDATER_PRIVATE_KEY` and publishes the
release — installed apps pick it up through the updater.

## Tests

```sh
go test ./...
```
