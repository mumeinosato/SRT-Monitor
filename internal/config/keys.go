package config

import (
	"fmt"
	"reflect"
	"strings"
)

// 設定を「input.port」のようなドット区切りのキーで読み書きする。
// キーは TOML のキー(構造体の toml タグ)と同じ。GUI(Web 画面)が、項目ごとの
// アクセサを書かずに、スキーマだけで画面を作れるようにするための仕組み。

func fieldByKey(v reflect.Value, key string) (reflect.Value, bool) {
	cur := v
	for _, part := range strings.Split(key, ".") {
		if cur.Kind() != reflect.Struct {
			return reflect.Value{}, false
		}
		found := false
		t := cur.Type()
		for i := 0; i < t.NumField(); i++ {
			tag := strings.Split(t.Field(i).Tag.Get("toml"), ",")[0]
			if tag == part {
				cur, found = cur.Field(i), true
				break
			}
		}
		if !found {
			return reflect.Value{}, false
		}
	}
	return cur, true
}

// Get は key の値を返す(int、float64、string、bool、[]string のどれか)。無ければ ok=false。
func Get(c Config, key string) (any, bool) {
	f, ok := fieldByKey(reflect.ValueOf(c), key)
	if !ok {
		return nil, false
	}
	switch f.Kind() {
	case reflect.Slice:
		out := make([]string, f.Len())
		for i := range out {
			out[i] = f.Index(i).String()
		}
		return out, true
	default:
		return f.Interface(), true
	}
}

// Set は key へ値を入れる。JSON から来た値(数値は float64、配列は []any)も受ける。
// 型が合わない、整数でない数を整数の項目へ入れる、などはエラー(範囲の検証は Validate が行う)。
func Set(c *Config, key string, val any) error {
	f, ok := fieldByKey(reflect.ValueOf(c).Elem(), key)
	if !ok {
		return fmt.Errorf("未知のキー: %s", key)
	}
	switch f.Kind() {
	case reflect.Int:
		n, ok := toFloat(val)
		if !ok || n != float64(int64(n)) {
			return fmt.Errorf("%s: 整数でなければならない", key)
		}
		f.SetInt(int64(n))
	case reflect.Float64:
		n, ok := toFloat(val)
		if !ok {
			return fmt.Errorf("%s: 数値でなければならない", key)
		}
		f.SetFloat(n)
	case reflect.String:
		s, ok := val.(string)
		if !ok {
			return fmt.Errorf("%s: 文字列でなければならない", key)
		}
		f.SetString(s)
	case reflect.Bool:
		b, ok := val.(bool)
		if !ok {
			return fmt.Errorf("%s: 真偽値でなければならない", key)
		}
		f.SetBool(b)
	case reflect.Slice:
		var items []string
		switch a := val.(type) {
		case []string:
			items = a
		case []any:
			for _, x := range a {
				s, ok := x.(string)
				if !ok {
					return fmt.Errorf("%s: 文字列の配列でなければならない", key)
				}
				items = append(items, s)
			}
		case nil:
		default:
			return fmt.Errorf("%s: 配列でなければならない", key)
		}
		f.Set(reflect.ValueOf(append([]string{}, items...)))
	default:
		return fmt.Errorf("%s: 設定できない型", key)
	}
	return nil
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// ---- 画面のスキーマ ----

// 項目の種類。
const (
	KindInt    = "int"
	KindFloat  = "float"
	KindString = "string"
	KindBool   = "bool"
	KindEnum   = "enum"
	KindArgs   = "args"  // 空白区切りの引数列([]string)
	KindTiles  = "tiles" // タイルの選択([]string、AllTiles の部分集合)
)

// Field は設定画面の項目1つ。Key が TOML のキーと1対1。
type Field struct {
	Tab     string   `json:"tab"`
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Kind    string   `json:"kind"`
	Enum    []string `json:"enum,omitempty"`
	Live    bool     `json:"live"`              // 「即時」の項目。それ以外は「適用して再起動」で反映
	Derived string   `json:"derived,omitempty"` // 真偽値のキー。それが真の間は、自動で決まる値を見せて無効にする
}

// Schema は設定画面の全項目(タブの順、タブ内の順)。
func Schema() []Field {
	const (
		conn = "接続"
		vid  = "映像"
		mon  = "監視"
		disp = "表示"
	)
	mtx := "mediamtx.enabled"
	return []Field{
		{Tab: conn, Key: "mediamtx.enabled", Label: "同梱のMediaMTXを使う(オフなら外部のMediaMTX)", Kind: KindBool},
		{Tab: conn, Key: "mediamtx.srt_listen", Label: "MediaMTX SRT待ち受け(host:port)", Kind: KindString},
		{Tab: conn, Key: "mediamtx.metrics_listen", Label: "MediaMTX /metrics待ち受け", Kind: KindString},
		{Tab: conn, Key: "mediamtx.log_level", Label: "MediaMTX ログレベル", Kind: KindEnum, Enum: []string{"error", "warn", "info", "debug"}},
		{Tab: conn, Key: "mediamtx.ready_timeout_ms", Label: "MediaMTX 準備完了の待ち(ms)", Kind: KindInt},
		{Tab: conn, Key: "mediamtx.hang_ms", Label: "MediaMTX 固まり判定(/metrics無応答, ms)", Kind: KindInt},
		{Tab: conn, Key: "input.host", Label: "入力先ホスト", Kind: KindString, Derived: mtx},
		{Tab: conn, Key: "input.port", Label: "入力先ポート", Kind: KindInt, Derived: mtx},
		{Tab: conn, Key: "input.path", Label: "パス名", Kind: KindString},
		{Tab: conn, Key: "input.mode", Label: "SRTモード", Kind: KindEnum, Enum: []string{"caller", "listener"}},
		{Tab: conn, Key: "input.latency_ms", Label: "レイテンシ(受信バッファの遅延, ms)", Kind: KindInt},
		{Tab: conn, Key: "input.timeout_ms", Label: "無通信タイムアウト(ms)", Kind: KindInt},
		{Tab: conn, Key: "input.connect_timeout_ms", Label: "接続タイムアウト(ms)", Kind: KindInt},
		{Tab: conn, Key: "input.recv_buffer_bytes", Label: "受信バッファのサイズ(byte, 0=libsrtの既定)", Kind: KindInt},
		{Tab: conn, Key: "input.probesize_bytes", Label: "入力解析の上限(byte。キーフレーム間隔が長い送信は大きく)", Kind: KindInt},
		{Tab: conn, Key: "input.analyzeduration_ms", Label: "入力解析の時間の上限(ms)", Kind: KindInt},
		{Tab: conn, Key: "input.tlpktdrop", Label: "遅れたパケットの破棄(tlpktdrop)", Kind: KindBool},

		{Tab: vid, Key: "video.width", Label: "出力解像度 幅", Kind: KindInt},
		{Tab: vid, Key: "video.height", Label: "出力解像度 高さ", Kind: KindInt},
		{Tab: vid, Key: "video.fps", Label: "表示間隔のFPS(バッファ使用時)", Kind: KindInt, Live: true},
		{Tab: vid, Key: "video.hwaccel", Label: "ハードウェアデコード", Kind: KindEnum, Enum: []string{"none", "vaapi", "qsv", "v4l2m2m"}},
		{Tab: vid, Key: "video.player_buffer_frames", Label: "プレイヤー側のバッファ(フレーム数, 0=最新優先)", Kind: KindInt, Live: true},
		{Tab: vid, Key: "video.extra_args", Label: "ffmpegの追加引数(上級者向け)", Kind: KindArgs},

		{Tab: mon, Key: "supervisor.watchdog_min_ms", Label: "ウォッチドッグのしきい値の下限(ms)", Kind: KindInt, Live: true},
		{Tab: mon, Key: "supervisor.start_timeout_ms", Label: "ffmpeg 起動から最初のフレームまでの待ち(キーフレーム待ち, ms)", Kind: KindInt, Live: true},
		{Tab: mon, Key: "supervisor.watchdog_latency_factor", Label: "ウォッチドッグ: レイテンシの倍率", Kind: KindFloat, Live: true},
		{Tab: mon, Key: "supervisor.backoff_initial_ms", Label: "再起動の待ち時間 開始(ms)", Kind: KindInt, Live: true},
		{Tab: mon, Key: "supervisor.backoff_max_ms", Label: "再起動の待ち時間 上限(ms)", Kind: KindInt, Live: true},
		{Tab: mon, Key: "supervisor.backoff_reset_after_ms", Label: "待ち時間を初期値へ戻すまでの安定時間(ms)", Kind: KindInt, Live: true},
		{Tab: mon, Key: "supervisor.hw_fallback_fail_ms", Label: "HWデコード: 起動失敗とみなす時間(ms)", Kind: KindInt, Live: true},
		{Tab: mon, Key: "supervisor.hw_fallback_fail_count", Label: "HWデコード: 外すまでの連続失敗回数", Kind: KindInt, Live: true},
		{Tab: mon, Key: "metrics.url", Label: "/metrics のURL", Kind: KindString, Live: true, Derived: mtx},
		{Tab: mon, Key: "metrics.interval_ms", Label: "/metrics の取得間隔(ms)", Kind: KindInt, Live: true},
		{Tab: mon, Key: "metrics.timeout_ms", Label: "/metrics の取得タイムアウト(ms)", Kind: KindInt, Live: true},
		{Tab: mon, Key: "metrics.reset_interval_ms", Label: "累計の自動リセット間隔(ms、0=しない、既定 5分)", Kind: KindInt, Live: true},

		{Tab: disp, Key: "display.monitor", Label: "表示するモニター(primary またはコネクター名)", Kind: KindString, Live: true},
		{Tab: disp, Key: "display.fullscreen", Label: "全画面", Kind: KindBool, Live: true},
		{Tab: disp, Key: "display.show_state_badge", Label: "状態バッジを出す", Kind: KindBool, Live: true},
		{Tab: disp, Key: "display.no_signal", Label: "無信号時の表示", Kind: KindEnum, Enum: []string{"last_frame", "black", "image"}, Live: true},
		{Tab: disp, Key: "display.no_signal_image", Label: "無信号時の画像(PNGのパス)", Kind: KindString, Live: true},
		{Tab: disp, Key: "overlay.enabled", Label: "オーバーレイを出す", Kind: KindBool, Live: true},
		{Tab: disp, Key: "overlay.position", Label: "オーバーレイの位置", Kind: KindEnum, Enum: []string{"top", "bottom"}, Live: true},
		{Tab: disp, Key: "overlay.tile_scale", Label: "タイルの大きさ(0.5〜3.0)", Kind: KindFloat, Live: true},
		{Tab: disp, Key: "overlay.opacity", Label: "透過度(0.1〜1.0)", Kind: KindFloat, Live: true},
		{Tab: disp, Key: "overlay.hotkey", Label: "ホットキー(表示の切り替え, 1文字)", Kind: KindString, Live: true},
		{Tab: disp, Key: "overlay.tiles", Label: "表示するタイル", Kind: KindTiles, Live: true},
	}
}
