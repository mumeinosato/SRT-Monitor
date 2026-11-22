# Raspberry Pi で使う(lite 版)

**状態: 実機(Pi 3B+、Bookworm 32bit)で動かした(2026-11-16)。** 1024×576・30fps の受信と、5秒の断の吸収を確かめた。
実機での構成と測定は、下の「実機での構成」の節。送信側(OBS)の設定は [`OBS.md`](OBS.md)。
「入れ方」と「推奨の設定」は、最初の見込み(デスクトップ起動)で、実機の結果とは異なる。**実機の構成を優先する。**

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
  コンソール起動(Lite)は、フレームバッファ(`/dev/fb0`)へ直接描く経路で対応した(実機の構成を参照。**Pi 3 では、こちらが必須**)。
- 操作画面は、ブラウザで `http://127.0.0.1:8088/` を開きます(ログに URL が出ます)。別の機械のブラウザから使うときは、
  `srtrx -web 0.0.0.0:8088 -web-token <合言葉>` で起動し、`http://<Pi のアドレス>:8088/?token=<合言葉>` を開きます。**トークン無しで、LAN へ公開しないでください。**

## 推奨の設定(最初の見込み。実機の値は「実機での構成」)

Pi 3B+ は、1080p30 の生フレーム(bgr0 で約250MB/s)を扱えません。次の設定にします。

| 項目(操作画面の「映像」「表示」) | 推奨 | 理由 |
| --- | --- | --- |
| 出力解像度(`video.width` × `video.height`) | **1280 × 720**(足りなければ 960 × 540) | 生フレームは 720p で約110MB/s、540p で約62MB/s。画面と同じ大きさにすると、窓いっぱいに出る |
| ハードウェアデコード(`video.hwaccel`) | `v4l2m2m`(H.264) | VideoCore の H.264 デコーダ。`/dev/video10` が使えること(ユーザーを `video` グループへ) |
| プレイヤー側のバッファ(`video.player_buffer_frames`) | 0 | 最新優先。遅延を溜めない |
| 解析の上限、無通信タイムアウト | 既定のまま | 次のキーフレームまで待つため |

**送信側(OBS)**: キーフレーム間隔は 1〜2秒、解像度 1280×720 以下、ビットレートは 4〜6Mbps、H.264(Baseline/Main)。
Pi 3B+ のハードウェアデコーダは、High プロファイルや、高ビットレートに弱い場合があります。

## 実機での構成(2026-11-16、Pi 3B+、Raspberry Pi OS Bookworm 32bit、v1.0.3 + 後述の修正)

**デスクトップを使わず、コンソール起動して、フレームバッファ(`/dev/fb0`)へ直接描く構成**にした。
デスクトップ(labwc + Xwayland)は、Pi 3 では CPU とメモリ帯域を食い、映像が 9〜10fps に落ちた。送信側の設定は [`OBS.md`](OBS.md)。

| 構成 | 受信 FPS(4Mbps、30fps の送信) |
| --- | --- |
| デスクトップ起動、2048×1152 表示、映像 1280×720 | 9〜10 |
| 画面を 1280×720 に下げる | 14 |
| コンソール起動 + `/dev/fb0`(16bpp) | 19〜24 |
| 同、`video=…-32`(32bpp)+ ffmpeg のログ抑制 | 18〜22 |
| 同、**1024×576**(画面も OBS も同じ) | **28〜35** |

ffmpeg 1プロセスの負荷の内訳(1コア=100%): SRT 受信が約10%、`h264_v4l2m2m` のデコードが約15%、`yuv420p→bgr0` の色変換(NEON 非対応の C 実装)が約45%、残りが出力のコピー。
**画素数が支配的**なので、解像度を下げるのが最も効く。

### Pi 側の設定

1. **コンソール起動にする**(デスクトップ無し。ブートターゲットを変える):

   ```bash
   sudo systemctl set-default multi-user.target
   ```

2. **`/boot/firmware/cmdline.txt`**(1行のまま、末尾などに足す。**事前に退避する**: `sudo cp /boot/firmware/cmdline.txt{,.orig}`):

   | 追加・変更 | 意味 |
   | --- | --- |
   | `video=HDMI-A-1:1024x576-32@60` | フレームバッファを、1024×576、32bpp にする(映像と等倍。`/sys/class/drm/card*-HDMI-A-1/modes` に出る値から選ぶ) |
   | `logo.nologo` | 起動ロゴを出さない |
   | `consoleblank=0` | コンソールを、一定時間で消さない |
   | `splash plymouth.ignore-serial-consoles` を削除 | 起動画面を出さない(**起動ログと、ログイン画面は、そのまま出す**) |

   **`console=tty1`、`getty@tty1`(ログイン画面)は、変えない。** 以前、`console=tty3` と `loglevel=0` と `getty@tty1` の無効化で画面を静かにしたら、起動が途中で止まったように見え、ログインもできなくなった。
3. **`/etc/sysctl.d/99-srtrx.conf`**(`sudo sysctl --system` で反映。番号は、Raspberry Pi OS の既定(`98-`)より後ろにする):

   ```
   net.core.rmem_max = 33554432
   net.core.rmem_default = 4194304
   kernel.printk = 0 0 0 0
   ```

   既定の UDP 受信バッファは約208KB で、5秒の断のあとの再送バースト(約2.5MB)を受け止められない。`printk` は、**起動が終わったあと**のカーネルログ(電圧低下の警告など)を、映像の上へ出さない(起動中のログは出る)。カーソルは、`srtrx-run` が実行中だけ隠す。

4. **CPU ガバナーを `performance`**: `/etc/systemd/system/cpu-performance.service`(oneshot で `scaling_governor` に `performance` を書く)を `enable`。
5. 使わないサービスを止める: `sudo systemctl disable --now bluetooth hciuart ModemManager cups cups-browsed triggerhappy`。

### アプリの設定(`~/.config/srtrx/config.toml`)

```toml
[input]
latency_ms = 1000
recv_buffer_bytes = 33554432

[video]
width = 1024
height = 576
fps = 30
hwaccel = "v4l2m2m"
extra_args = ["-loglevel", "error", "-filter_threads", "4"]
player_buffer_frames = 0

[overlay]
enabled = true
```

- `extra_args` の `-loglevel error`: ffmpeg は、`No accelerated colorspace conversion…` を毎フレーム警告し、ログ(journald)に CPU を使うので、抑える。
- `width × height` は、画面(フレームバッファ)と OBS の出力解像度に**そろえる**。

### 起動と終了(systemd は使わない)

[`scripts/srtrx-run`](../scripts/srtrx-run) を `~/.local/bin/` へ置く(`install -m755 scripts/srtrx-run ~/.local/bin/`)。

```bash
srtrx-run          # 前面で起動。Ctrl+C で、プレイヤー、制御、MediaMTX、ffmpeg をまとめて終了する
```

- `$DISPLAY` を空にして、プレイヤーがフレームバッファへ描くようにする。
- 操作画面(Web)を、`0.0.0.0:8088`、合言葉 `nc` で公開する: `http://<Pi のアドレス>:8088/?token=nc`。**LAN の外へ出さない**。変えるなら、`srtrx-run -web <host:port> -web-token <合言葉>`(後ろの引数が優先される)。
- 起動と終了の確認: `SIGINT`(Ctrl+C)と `SIGTERM` のどちらでも、4つのプロセスが残らない。

### 元に戻すには

```bash
sudo systemctl set-default graphical.target
sudo systemctl enable getty@tty1.service
sudo cp /boot/firmware/cmdline.txt.orig /boot/firmware/cmdline.txt
sudo reboot
```

### 5秒の断

OBS の URL に `latency=6000000` を付けて、OBS との通信を止める試験をした(手順と結果の表は [`OBS.md`](OBS.md) の6)。
**3秒は 3/3、4秒は 4/4(最大フレーム遅延 44ms、ffmpeg の再起動 0)で、映像は止まらなかった。5秒は 3/4 で、止まる回があった**(確実なのは約4秒まで)。
限界は2つ: (1) OBS の SRT が、libsrt の既定の5秒無通信で接続を切り、再接続のあいだ映像が止まる(OBS の URL からは変えられない)。(2) 断のあと、溜まった映像が一気に流れ込み、Pi 内の ffmpeg←MediaMTX の SRT が `SEQUENCE DISCREPANCY` で切れて、ffmpeg が再起動する(約3秒)。
(2) は、`input.latency_ms=1000` と `input.recv_buffer_bytes=33554432` で、4秒では起きなくなった。

### 修正した不具合

送信が無い間、MediaMTX は読み取りを拒否し、ffmpeg が起動直後に終了する。これを「ハードウェアデコードの起動失敗」と数え、3回目に**ソフトウェアデコードへ誤って落ちていた**(`internal/supervisor`)。
SRT の接続失敗(`Peer rejected connection`、`Connection to srt://…`)は、数えないようにした(回帰テスト `TestHWFallbackIgnoresSRTConnectFailure`)。

### 残っている注意

- **サーマルスロットリング**(70℃前後、`vcgencmd get_throttled` = `0x80008`)。ヒートシンクかファンを付ける。
- **電源**: 過去に電圧低下を検知(`0x80000` 系)。5V/2.5A 以上の電源にする。
- **OBS の PC が Wi-Fi だと、約10%のパケットロス**が出た(再送で回復する)。有線にする。
- 画面の見た目(カクつきの主観評価)は、数値(FPS、フレーム経過時間)でしか確かめていない。

## 実機で確かめること(1・3・4・6 は実施済み。5 は未実施)

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
