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
| 2026-11-08 | UIのモダン化 | libadwaita は使わず、GTK4 + CSS(暗色テーマ、カード、ピル型タブ、スイッチ)。色は `@define-color` に集める。統計タイルはプレイヤーと同じ `player.Snapshot.Tiles` を使う | gotk4 の既定の依存だけで済み(判断規則6)、オーバーレイと制御画面で値と色の規則が食い違わない |
| 2026-11-08 | オーバーレイの文言 | ラベルと値を英語にする(CONNECTION / DROPPED / LOSS / RETRANS / FRAME ERRORS / RTT / FPS / NO SIGNAL / RESTARTS、Connected / Offline、バッジは Connecting / Reconnecting / Stopped / Control lost)。大きさも一回り小さくした | ユーザーの指示(日本語だと野暮ったい、表示が大きい)。仕様書のタイルの日本語名は、色の規則と元の指標は変えずに置き換えた。制御GUIの文言は日本語のまま |
| 2026-11-08 | ffmpeg の起動待ち | `supervisor.start_timeout_ms`(既定15秒)を新設し、起動から最初のフレームまでの待ちを、ウォッチドッグのしきい値(3秒)から分ける。ウォッチドッグより短くはしない | 実機のログで、`起動しても 3s の間、準備完了にならない` を永久に繰り返した。送信の途中から繋ぐと、次のキーフレームまで映像が出ない(実測: キーフレーム間隔 1秒→2.1秒、4秒→7.5秒、8秒→11.5秒)。仕様の「しきい値の間フレームが来ない」を、起動時だけ緩めた。動作中のウォッチドッグは仕様どおり3秒 |
| 2026-11-08 | ffmpeg の入力解析の上限 | `-probesize` と `-analyzeduration` を設定(`input.probesize_bytes`、`input.analyzeduration_ms`)にし、既定を 32768/0 から 524288(512KB)/1000ms にした | 実機(ffmpeg 8.0.1)のログで `Could not find codec parameters ... unspecified size` → ffmpeg が自分から終了。32KB は、最初のキーフレーム1枚(1080p/6Mbpsで150〜300KB)も読み切れない。大きくしすぎると、ffmpeg が上限まで読み続けて起動が遅れる。実測(同じ物差しでの T4 復帰時間): 32KB=5.1秒、512KB=5.4秒(+0.3)、1MB=6.9秒(+1.8)、10MB=約+10秒。再起動のたびに効くので、遅れの小さい 512KB(1080p で約10Mbps までの最初のキーフレームを読み切れる)に留めた。**ffmpeg 8 では未検証**(手元は 6.1.1) |
| 2026-11-08 | ffmpeg の映像の選択 | 出力の前に `-map 0:v:0` を付け、映像ストリームを明示して選ぶ | 実機(ffmpeg 8.0.1、OBS のキーフレーム間隔8秒)で、v0.1.1 でも `Could not find codec parameters ... unspecified size` → `Output file does not contain any stream` で終了し続け、映像が出るまで約4分かかった。解析の時間(1秒)の間にキーフレームが来ないと、自動の選択が寸法不明の映像を選ばないため。MPEG-TS では解析は常に `analyzeduration` の上限まで読み続ける(実測: 30秒にすると、キーフレームが3秒で来ても30秒待つ)ので、上限を大きくする対処は使えない。明示して選べば、解析で寸法が分からなくてもデコーダが次のキーフレームを待って映像を出す(実機で12回中12回、キーフレーム到着の直後に最初のフレーム)。解析の上限の既定(512KB/1秒)は変えない |
| 2026-11-08 | SRT の無通信タイムアウト | 既定を 5秒 から 10秒 に変更(`input.timeout_ms`) | キーフレーム間隔が8秒の送信で、新規の接続が無通信 5秒で切れた(ffmpeg の終了コード 146)。15秒にすると通る。ffmpeg が繋ぐのは手元の MediaMTX(ループバック)で、動作中の停止はウォッチドッグ(3秒)が先に検知するので、障害検知は遅くならない。T11 の15秒断は、10秒でも超える。仕様の表の既定(5秒)からの変更 |
