# claude-mobile-agent

Bridge your Mac's [Claude Code](https://docs.claude.com/claude-code) sessions to your iPhone over WebSocket. Read messages, drive the prompt, send `Esc` — all from your phone, while Claude keeps running in tmux on your Mac.

```
Claude CLI (tmux pane) → jsonl tail → daemon → relay (VPS) → iPhone web
```

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/chaohaowang/claude-mobile-agent/main/install.sh | bash
```

Installs the latest `claude-mobile` binary into `~/.local/bin/`. Or grab a tarball from the [releases](https://github.com/chaohaowang/claude-mobile-agent/releases) page and extract it yourself.

## Quick start

```bash
# 1. Pair your iPhone — prints a QR code + URL.
claude-mobile pair

#    Scan with the iPhone camera (or open the URL in Safari) and bookmark it.

# 2. Start a tmux session running Claude:
tmux new-session -d -s work 'claude'

# 3. Run the daemon in the foreground:
claude-mobile daemon
```

The daemon auto-discovers any tmux pane running `claude` and exposes them all as tabs in the iPhone web client.

## Requirements

- macOS 13+ (Apple Silicon or Intel)
- `tmux` — `brew install tmux`
- `claude` CLI — Anthropic [Claude Code](https://docs.claude.com/claude-code)

## Configuration

Auto-generated on first run at `~/.config/claude-mobile/host.toml`:

```toml
host_id    = "host-xxxxxxxxxxxx"        # random per-Mac id
relay_url  = "ws://47.79.84.115:8443/ws"  # shared relay (default)
public_url = "http://47.79.84.115:8443"   # baked into the pair QR
```

Edit `relay_url` / `public_url` to point at your own relay.

## Optional: voice input (ASR)

If you want to dictate prompts from the phone, export an Aliyun Bailian key before starting the daemon:

```bash
export BAILIAN_API_KEY="sk-xxxxxxxx"
claude-mobile daemon
```

The daemon uses `qwen3-asr-flash`. Without the key, ASR returns "not configured" to the phone; everything else still works.

## Troubleshooting

**`"claude-mobile" can't be opened` (Gatekeeper)** — the binary isn't notarized. The installer pipes via `curl` and avoids quarantine, but if you downloaded a tarball through Safari:

```bash
xattr -d com.apple.quarantine ~/.local/bin/claude-mobile
```

Or right-click → Open once, then macOS remembers it.

**`command not found: claude-mobile`** — add `~/.local/bin` to your `PATH`:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc
source ~/.zshrc
```

**iPhone shows blank or stuck** — check the daemon is alive:

```bash
pgrep -fl 'claude-mobile daemon'
```

Restart it: `pkill -f 'claude-mobile daemon'; claude-mobile daemon &`.

## License

MIT — see [LICENSE](LICENSE).
