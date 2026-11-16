## 入っているもの

| ファイル | 内容 |
| --- | --- |
| `srtrx-<版>-linux-amd64.tar.gz` | **本番向けの既定の版。** GTK4 の画面(x86_64、Ubuntu 24.04 でビルド) |
| `srtrx-<版>-lite-linux-amd64.tar.gz` | 実験版。GTK を使わない(cgo なし)。x86_64 |
| `srtrx-<版>-lite-linux-armv7.tar.gz` | 実験版。Raspberry Pi 2/3/3B+/4 の 32bit OS(Raspberry Pi OS Legacy 32-bit など) |
| `srtrx-<版>-lite-linux-arm64.tar.gz` | 実験版。arm64 |

`*.sha256` は、それぞれのチェックサムです。

## 動作環境

- **GTK 版**: Ubuntu 24.04 LTS (x86_64)。GTK 4.10 以上
- **lite 版**: Linux。画面は X11 が要ります(Wayland のみの環境は、XWayland 経由)。操作画面は、同じ機械のブラウザで開きます(`http://127.0.0.1:8088/`)
- ffmpeg(**libsrt 有効**のビルド)。GTK 版は 6.0 以上、lite 版は 5.0 以上(5.x は動作未確認)。起動時に自己診断します
- MediaMTX は `srtrx` に埋め込み済み(公式バイナリ、MIT。`LICENSE-mediamtx` を同梱)

## 入れ方

```bash
tar xzf srtrx-*.tar.gz && cd srtrx-*
install -Dm755 srtrx srtrx-player -t ~/.local/bin
install -Dm644 srtrx.service srtrx-player.service -t ~/.config/systemd/user
systemctl --user daemon-reload
systemctl --user enable --now srtrx-player.service srtrx.service
```

lite 版は、ログに `操作画面(Web)` の URL が出ます。Raspberry Pi の設定は `docs/RASPBERRY_PI.md` を参照してください。
