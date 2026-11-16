# Raspberry Pi で使う(lite 版)

**状態: ビルドと、qemu 上での起動の確認まで。実機の Raspberry Pi では、まだ動かしていません。**
ここに書く推奨設定は、設計と x86 での測定からの見込みで、実機で確かめて調整してください。

## 対象

| 項目 | 内容 |
| --- | --- |
| 機種 | Raspberry Pi 3B+(Cortex-A53 1.4GHz、1GB)。Pi 2/3/4 の 32bit OS は armv7 版。arm64 の OS は arm64 版 |
| OS | Raspberry Pi OS (Legacy) = **Debian Bookworm**(2026-11 時点の公式一覧で確認)。Trixie でも同じ |
| 版 | リリースの `srtrx-<版>-lite-linux-armv7.tar.gz`(32bit)または `…-arm64.tar.gz`(64bit) |
| 対象外 | Pi Zero / Pi 1(armv6)。GTK 版(Bookworm の GTK は 4.8 で、要求の 4.10 に満たない) |

「同じバイナリ」ではなく、**同じソース・同じ機能**を、CPU ごとにビルドしたものです(x86_64 と ARM は、1つの実行ファイルでは動かせません)。
lite 版は cgo を使わない純 Go で、`make build-lite GOARCH=arm GOARM=7` だけで、どの機械でもクロスビルドできます。

## 入れ方

```bash
sudo apt install ffmpeg          # Bookworm は 5.1 系。libsrt が有効なこと
ffmpeg -hide_banner -protocols | grep -w srt     # srt が出ること

tar xzf srtrx-*-lite-linux-armv7.tar.gz && cd srtrx-*
install -Dm755 srtrx srtrx-player -t ~/.local/bin
install -Dm644 srtrx.service srtrx-player.service -t ~/.config/systemd/user
systemctl --user daemon-reload
systemctl --user enable --now srtrx-player.service srtrx.service
```

- **画面は X11 が要ります。** Raspberry Pi OS の既定のデスクトップ(Wayland の labwc)では、XWayland 経由で動きます(`$DISPLAY` が設定されていること)。
  コンソール起動(Lite)は、まだ対応していません。
- 操作画面は、ブラウザで `http://127.0.0.1:8088/` を開きます(ログに URL が出ます)。別の機械のブラウザから使うときは、
  `srtrx -web 0.0.0.0:8088 -web-token <合言葉>` で起動し、`http://<Pi のアドレス>:8088/?token=<合言葉>` を開きます。**トークン無しで、LAN へ公開しないでください。**

## 推奨の設定(Pi 3B+ の見込み)

Pi 3B+ は、1080p30 の生フレーム(bgr0 で約250MB/s)を扱えません。次の設定にします。

| 項目(操作画面の「映像」「表示」) | 推奨 | 理由 |
| --- | --- | --- |
| 出力解像度(`video.width` × `video.height`) | **1280 × 720**(足りなければ 960 × 540) | 生フレームは 720p で約110MB/s、540p で約62MB/s。画面と同じ大きさにすると、窓いっぱいに出る |
| ハードウェアデコード(`video.hwaccel`) | `v4l2m2m`(H.264) | VideoCore の H.264 デコーダ。`/dev/video10` が使えること(ユーザーを `video` グループへ) |
| プレイヤー側のバッファ(`video.player_buffer_frames`) | 0 | 最新優先。遅延を溜めない |
| 解析の上限、無通信タイムアウト | 既定のまま | 次のキーフレームまで待つため |

**送信側(OBS)**: キーフレーム間隔は 1〜2秒、解像度 1280×720 以下、ビットレートは 4〜6Mbps、H.264(Baseline/Main)。
Pi 3B+ のハードウェアデコーダは、High プロファイルや、高ビットレートに弱い場合があります。

## 実機で確かめること(未実施)

1. `ffmpeg -hide_banner -version` が 5.0 以上で、srt が有効。起動時のログに `自己診断に合格` が出る(5.x は「動作確認していない」と警告が出る)。
2. T3(レイテンシの反映): `input.latency_ms=500` で `curl -s localhost:9998/metrics | grep ms_receive_tsb_pd_delay` が 500。
   (ffmpeg 5.1 の SRT のミリ秒/マイクロ秒の扱いは、6.1.1 と 8.0.1 でしか確かめていません。違えばここで分かります。)
3. T2(基本表示): 5分間、状態画面の実測FPSが 29 以上。足りなければ、解像度を下げる。
4. `v4l2m2m` で映像が出ること。出なければ、ログの ffmpeg の stderr を確認する。起動失敗が3回続くと、自動でソフトウェアデコードへ落ちる(状態画面に表示)。
5. T4〜T9、T13〜T16 の、通常の受け入れ試験(`docs/VERIFY.md`)。
6. CPU 使用率と、メモリ(`top`)。controller は埋め込みの MediaMTX を、`~/.local/state/srtrx/bin/` へ書き出す(約40MB)。

## 重ければ

- 外部の MediaMTX(別の機械)を使い、Pi では ffmpeg とプレイヤーだけを動かす(`mediamtx.enabled=false`)。
- 解像度をさらに下げる(854×480、640×360)。タイルは、幅に合わせて自動で縮む。
- `overlay.enabled=false` で、タイルを止める(合成のコストが無くなる。バッジは出る)。
