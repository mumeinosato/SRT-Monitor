# DECISIONS

実装中に決めたことの記録。1行ずつ(日付、項目、決めたこと、理由)。

| 日付 | 項目 | 決めたこと | 理由 |
| --- | --- | --- | --- |
| 2026-11-08 | モジュール名 | `srtrx`。リポジトリ直下に `go.mod` を置く | 仕様のリポジトリ構成(`srtrx/`)に合わせ、モジュールは1つにする |
| 2026-11-08 | gotk4 の版 | `github.com/diamondburned/gotk4/pkg v0.3.1`(v0.4.1 は使わない) | v0.4.1 は新しい glib(`g_get_monotonic_time_ns` など)を前提とし、基準OSの Ubuntu 24.04(glib 2.80)でcgoのビルドが通らない。v0.3.1 は通る |
| 2026-11-08 | 追加のビルド依存 | `libgirepository1.0-dev`(gobject-introspection) | gotk4 のcgoが要求する。CIと README に書く |
| 2026-11-08 | Go の版 | `go.mod` の go 指令は依存(prometheus/common)が要求する版に従う。`GOTOOLCHAIN=auto` で取得される | 仕様は1.22以上。新しい依存を避けるより、標準の仕組みに任せる方が壊れにくい |
| 2026-11-08 | ffmpeg の引数の並び | `-i URL -vf ... {extra_args} -an -sn -dn -fps_mode passthrough -f rawvideo OUT` | 仕様の「出力」節の規則(`-an -sn -dn ...` を最後に付け、`extra_args` はその直前)を、冒頭の例より優先した。ffmpeg の解釈は同じ |
| 2026-11-08 | `latency`/`timeout` の単位 | URL へはミリ秒×1000(マイクロ秒)。`connect_timeout` のみミリ秒。変換は `internal/ffmpeg.BuildURL` の1か所 | 実測で確認済み(ffmpeg 6.1.1 + MediaMTX 1.21.3): `latency=500000` → `srt_conns_ms_receive_tsb_pd_delay{state="read"}` が 500。`latency=500` だと 120(既定へ丸め)になる |
| 2026-11-08 | 汎用の子プロセス管理 | `internal/proc` を追加し、ffmpeg と MediaMTX の両方が使う | 仕様に無い共通部品。Setpgid、Pdeathsig、行読み、停止、pid ファイルの重複を避ける |
| 2026-11-08 | Pdeathsig の落とし穴 | 子の fork は、`runtime.LockOSThread` した専用goroutineから必ず行う | `Pdeathsig` は「子を fork したスレッド」の終了で発火する。Go のスレッドは増減するため、素の `cmd.Start` だと、無関係なタイミングで子が殺される。`TestChildSurvivesThreadChurn` で確認 |
| 2026-11-08 | pid ファイルの安全策 | 片付けの前に `/proc/<pid>/cmdline` に期待する名前があるか確かめる | pid の再利用で無関係なプロセスを殺さないため |
| 2026-11-08 | 停止時の SIGCONT | SIGTERM のあと、続けて SIGCONT をグループへ送る | SIGSTOP 中(T5、T14)のプロセスは SIGTERM を受け取れず、猶予の1秒を丸ごと無駄にするため |
| 2026-11-08 | `internal/controller` | 制御の中核(設定、監督、統計、IPC)を GUI から分けて新設 | GUI を薄くし、`-headless` で GUI 無しでも動かせる(受け入れテストを GUI 無しの環境で回せる) |
| 2026-11-08 | `internal/logx` | slog のセットアップ、リングバッファ、`Recover` を置く | 仕様に無い共通部品。判断規則8(goroutine入口の recover)を1か所にまとめる |
| 2026-11-08 | MediaMTX の準備判定 | 制御が、起動した MediaMTX ごとに `/metrics` を250ms間隔で自前に叩く(統計の取得とは別) | 統計の取得間隔は最大10秒まで設定でき、準備完了(5秒)と固まり判定(10秒)に使うには粗い |
| 2026-11-08 | 統計の取得基準 | 「起動後の最初の取得成功時」に既にあった接続を基準にする | 最初の取得が失敗した場合も、成功した最初の時点を起動時とみなす(単純で壊れにくい) |
| 2026-11-08 | `/metrics` のラベル無しの行 | `srt_conns 0` のようなラベル無しの行は、接続・パスとして数えない | 実物(MediaMTX 1.21.3)は、接続が無いとき値0のラベル無しの行を出す |
| 2026-11-08 | 実行時ディレクトリ | `$XDG_RUNTIME_DIR` が無ければ `$TMPDIR/srtrx-run-<uid>/srtrx` | systemd のユーザーユニットでは必ず設定される。開発時の保険 |
| 2026-11-08 | ffmpeg の場所 | `$SRTRX_FFMPEG` があればそれを使う。無ければ PATH | テストと、PATH 外のビルドを使う場合のため |
| 2026-11-08 | プロファイル | `~/.config/srtrx/profiles/<名前>.toml`。読み書きは本体と同じ(世代保存を含む) | 仕様は「例: 本番、検証」のみ。最も単純な実装 |
| 2026-11-08 | 設定の保存 | 内容が現在のファイルと同一なら、世代をずらさない | 同じ内容を何度も保存して、正常な古い世代が押し出されるのを防ぐ |
| 2026-11-08 | 設定の検証の追加 | 仕様の範囲に加え、列挙値(`mode`、`hwaccel` など)、`watchdog_latency_factor`(1〜20)、`backoff_reset_after_ms`、`hw_fallback_*`、`mediamtx.*` の範囲を検証 | 仕様の表に無い項目も、壊れた値で起動しないようにする |
| 2026-11-08 | `state` の送出 | 自己診断に失敗している間は `backoff` と `detail="ffmpeg: <理由>"` を送る | 仕様は「この間も state を送り続ける」とのみ。既存の4状態で表す |
| 2026-11-08 | 統計リセットのホットキー | 制御GUIのウィンドウ内 `Ctrl+R` | プレイヤー→制御のメッセージは `hello`/`heartbeat` のみで、プレイヤーからリセットを頼めない。表示切り替えのホットキー(`overlay.hotkey`)はプレイヤー側 |
| 2026-11-08 | 「情報」画面 | 制御GUIのヘッダーのボタンから、MediaMTX のライセンスを別ウィンドウで表示 | 仕様は3画面(状態、設定、ログ)と「情報」を別に書いている |
| 2026-11-08 | モニター指定 `primary` | 全画面にするだけ(コンポジタが選ぶモニター)。コネクター名のときは、そのモニターで全画面 | GTK4 には「プライマリモニター」を得る共通のAPIが無い(X11とWaylandで違う) |
| 2026-11-08 | CI | `.github/workflows/ci.yaml` で `make vet`、`make test`、`make build`(ubuntu-24.04) | gotk4 の初回ビルドが長いため、ビルドと検証の場を増やす |
| 2026-11-08 | 累計のリセット | 制御プロセスへの `SIGUSR1` でもリセットできる | GUI無しの運用と、受け入れテスト(T12)のため。GUIのボタン、`Ctrl+R` と同じ処理 |
| 2026-11-08 | フレーム表示の実装 | `internal/player/shim.go`(cgo)で `gdk_memory_texture_new` と `gtk_picture_set_paintable` を直接呼ぶ | gotk4 経由で毎フレームテクスチャを作ると解放が追いつかず、30fpsで毎秒約180MBのリークになった(`docs/VERIFY.md`)。`GtkPicture` が最新の1枚だけを持ち、前のものは即座に解放される |
| 2026-11-08 | 描画のタイミング | フレームクロック(`AddTickCallback`)で最新の1枚を取る(仕様どおり) | タイマー方式でもリークは変わらなかったので、仕様を変えない |
| 2026-11-08 | MediaMTX の作業ディレクトリ | 設定ファイル(`mediamtx.yml`)と同じディレクトリで起動 | MediaMTX が作業ディレクトリへ自己署名の証明書(`auto.crt`/`auto.key`)を作るため。リポジトリや `$HOME` を汚さない |
