# claude-mobile-agent

把 Mac 上的 [Claude Code](https://docs.claude.com/claude-code) 终端会话桥接到 iPhone：手机上看消息、回消息、按 Esc 中断。Mac 这台机器一直跑着，但你人不需要在键盘前。

```
Claude CLI (tmux) → 守护进程 → relay (VPS) → iPhone web
```

## 前提

- macOS（Apple Silicon 或 Intel）
- `tmux` —— `brew install tmux`
- `claude` CLI —— [Anthropic Claude Code](https://docs.claude.com/claude-code)

## 安装

```bash
curl -fsSL https://raw.githubusercontent.com/chaohaowang/claude-mobile-agent/main/install.sh | bash
```

会装到 `~/.local/bin/claude-mobile`。如果终端找不到命令，把这行加到 `~/.zshrc` 然后 `source` 一下：

```bash
export PATH="$HOME/.local/bin:$PATH"
```

## 三步上手

**1. 配对手机**

```bash
claude-mobile pair
```

打印二维码 + URL。iPhone 相机扫一下，Safari 打开后**加到主屏幕**。

**2. 用 tmux 起 claude**（推荐加 `bypassPermissions`，不然手机端按不了 1/2/3 菜单）

```bash
tmux new-session -d -s work -c ~/some-project 'claude --permission-mode bypassPermissions'
```

`-s work` 是会话名（自己取，名字会显示在手机 tab 上）。多开几个都行：

```bash
tmux new-session -d -s blog -c ~/blog 'claude --permission-mode bypassPermissions'
```

**3. 跑守护进程**（前台一直开着）

```bash
claude-mobile daemon
```

打开手机上的图标，所有 tmux 里跑着的 claude 会话都会变成 tab。

## 常用 tmux 命令

```bash
tmux ls                  # 看你开了哪些会话
tmux attach -t work      # 在 Mac 上进入会话
# 进入后按 Ctrl-b 然后 d → 离开但保持 claude 继续跑
# 千万别 Ctrl-D 或 exit —— 那会退出 claude 把 pane 关掉
tmux kill-session -t work    # 彻底关
```

## 可选：语音输入（ASR）

想从手机上语音输入提示词，跑 daemon 前 export Aliyun 百炼 key：

```bash
export BAILIAN_API_KEY="sk-xxxxxxxx"
claude-mobile daemon
```

不设也行，只是手机上录音按钮会返回"未配置"。

## 配置

首次跑会自动生成 `~/.config/claude-mobile/host.toml`：

```toml
host_id    = "host-xxxxxxxxxxxx"          # 这台 Mac 的随机 ID
relay_url  = "ws://47.79.84.115:8443/ws"   # 默认走我（chaohaowang）的 relay，不存任何消息内容
public_url = "http://47.79.84.115:8443"    # 二维码里的 URL
```

想用自己的 relay 把这两个 URL 改了即可。

## 排查

**首次跑被 macOS 拦"无法验证开发者"** —— 二进制没公证。
```bash
xattr -d com.apple.quarantine ~/.local/bin/claude-mobile
```
或右键 `claude-mobile` → 打开 → 确认。

**手机空白 / 卡住** —— 守护进程可能挂了：
```bash
pgrep -fl 'claude-mobile daemon'        # 没输出说明没在跑
claude-mobile daemon &                   # 后台启动
```

**手机看不到某个会话** —— 守护进程每 2 秒扫一次 tmux pane。新开的会话稍等就会出现。检查 pane 里 claude 是不是还活着：`tmux attach -t <名字>`。

## License

MIT —— 见 [LICENSE](LICENSE)。
