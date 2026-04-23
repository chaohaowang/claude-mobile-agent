# claude-mobile-agent

Bridges a Claude Code tmux session on macOS to a WebSocket relay. MVP: no
crypto, no ASR. See `docs/superpowers/specs/2026-04-23-mobile-claude-terminal-bridge-design.md`
for the full design.

## Build

```bash
go build -o claude-mobile ./cmd/claude-mobile
```

## Manual smoke test

1. **Start a tmux session with Claude Code:**
   ```bash
   tmux new-session -s cm-smoke -d 'claude'
   ```

2. **Start a dummy WebSocket echo relay (in another terminal):**
   Install `websocat` (`brew install websocat`), then:
   ```bash
   websocat -s 0.0.0.0:9999
   ```
   This prints every frame the agent sends, and lets you paste frames
   back to it by typing JSON into stdin.

3. **Create config at `~/.config/claude-mobile/config.toml`:**
   ```toml
   [relay]
   url = "ws://localhost:9999"

   [session]
   tmux_target = "cm-smoke:0"
   cwd = "/Users/YOUR_USERNAME"   # the cwd Claude Code is running in
   name = "smoke"
   ```

4. **Start the daemon:**
   ```bash
   ./claude-mobile daemon
   ```

5. **Verify outbound:** attach to the tmux session in another window
   (`tmux attach -t cm-smoke`), type a message into Claude, press Enter.
   You should see a `session.message` frame print in the websocat terminal.

6. **Verify inbound:** in the websocat terminal, paste:
   ```
   {"type":"session.send","seq":1,"payload":{"session_id":"smoke","text":"hello from phone","is_slash":false,"request_id":"r1"}}
   ```
   The text `hello from phone` should appear in the Claude Code tmux pane.

7. **Clean up:**
   ```bash
   tmux kill-session -t cm-smoke
   ```

## Config reference

| Key | Default | Purpose |
|---|---|---|
| `relay.url` | (required) | WebSocket URL |
| `relay.reconnect_initial_sec` | 5 | initial backoff |
| `relay.reconnect_max_sec` | 60 | max backoff |
| `session.tmux_target` | (required) | `<session>:<window>` |
| `session.cwd` | (required) | cwd Claude Code is running in |
| `session.name` | `default` | session display name |
