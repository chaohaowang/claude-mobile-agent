# claude-mobile-agent

Bridges a Claude Code tmux session on macOS to a WebSocket relay so a phone
client can see and drive the session. See
`docs/superpowers/specs/2026-04-23-mobile-claude-terminal-bridge-design.md`
for the full design.

## Install

```bash
go build -o claude-mobile ./cmd/claude-mobile
# (optional) put on PATH so `cm` works from anywhere:
install -m 0755 claude-mobile /usr/local/bin/claude-mobile
echo "alias cm='claude-mobile start'" >> ~/.zshrc
```

## One-shot quick start

Once per setup, write `~/.config/claude-mobile/config.toml` with only the
[relay] section:

```toml
[relay]
url = "ws://47.79.84.115:8443/ws"    # your relay
pair_id = "my-pair"                   # any stable string, matches the phone
device_id = "mac-$(hostname -s)"      # for server-side logging
```

Then, in any project folder:

```bash
cd ~/some-project
claude-mobile start           # or: cm
```

This spawns a tmux session `cm-some-project` running
`claude --permission-mode bypassPermissions` in the cwd, then runs the bridge
daemon in the foreground. Ctrl-C stops the daemon; the tmux session stays
alive — reattach with `tmux attach -t cm-some-project`.

Override the permission mode:
```bash
claude-mobile start --permission-mode default
```

Target a different directory:
```bash
claude-mobile start ~/other-project
```

Rename the session:
```bash
claude-mobile start --name fe-work .
```

## Full `daemon` mode (explicit config)

Use when you want to drive an existing tmux session or script the daemon
without `start`'s conveniences. Requires both [relay] and [session] sections:

```toml
[relay]
url = "ws://47.79.84.115:8443/ws"
pair_id = "my-pair"
device_id = "mac-abc"

[session]
tmux_target = "cm-foo:0"
cwd = "/Users/me/foo"
name = "foo"
```

```bash
claude-mobile daemon
```

## Manual smoke test (no phone, uses websocat)

Install [websocat](https://github.com/vi/websocat): `brew install websocat`.

Terminal 1 — act as phone:
```bash
websocat -t "ws://47.79.84.115:8443/ws?pair=my-pair&role=phone&device=test"
```

Terminal 2 — start agent:
```bash
cd ~/some-project
claude-mobile start
```

In Terminal 1, paste a frame to drive Claude:
```json
{"type":"session.send","seq":1,"payload":{"session_id":"some-project","text":"hi","is_slash":false,"request_id":"r1"}}
```

Messages Claude emits (from the jsonl tail) flow back to Terminal 1.

## Config reference

| Key | Required by | Default | Purpose |
|---|---|---|---|
| `relay.url` | start, daemon | — | WebSocket URL |
| `relay.pair_id` | start, daemon | — | routing id, shared with phone |
| `relay.device_id` | start, daemon | — | server-side logging id |
| `relay.reconnect_initial_sec` | — | 5 | initial backoff |
| `relay.reconnect_max_sec` | — | 60 | max backoff |
| `session.tmux_target` | daemon only | — | `<sess>:<window>` |
| `session.cwd` | daemon only | — | cwd the Claude session runs in |
| `session.name` | daemon only | `default` | session display name |
