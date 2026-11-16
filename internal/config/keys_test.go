package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestGetSetRoundTrip(t *testing.T) {
	c := Default()
	cases := []struct {
		key string
		val any
	}{
		{"input.port", 9000.0}, // JSON の数値は float64
		{"input.mode", "listener"},
		{"input.tlpktdrop", false},
		{"overlay.opacity", 0.5},
		{"overlay.tiles", []any{"rtt", "fps"}},
		{"video.extra_args", []any{"-threads", "2"}},
		{"metrics.reset_interval_ms", 0.0},
	}
	for _, tc := range cases {
		if err := Set(&c, tc.key, tc.val); err != nil {
			t.Fatalf("%s: %v", tc.key, err)
		}
		got, ok := Get(c, tc.key)
		if !ok {
			t.Fatalf("%s: 取得できない", tc.key)
		}
		switch w := tc.val.(type) {
		case float64:
			if g, isInt := got.(int); isInt && float64(g) != w {
				t.Errorf("%s: %v != %v", tc.key, got, tc.val)
			}
		case []any:
			var ws []string
			for _, x := range w {
				ws = append(ws, x.(string))
			}
			if !reflect.DeepEqual(got, ws) {
				t.Errorf("%s: %v != %v", tc.key, got, ws)
			}
		default:
			if got != tc.val {
				t.Errorf("%s: %v != %v", tc.key, got, tc.val)
			}
		}
	}
	if c.Input.Port != 9000 || c.Overlay.Opacity != 0.5 || len(c.Overlay.Tiles) != 2 {
		t.Errorf("構造体へ反映される: %+v", c.Input)
	}
}

func TestSetRejectsWrongTypes(t *testing.T) {
	c := Default()
	for key, bad := range map[string]any{
		"input.port":      "9000",   // 文字列
		"input.mode":      5.0,      // 数値
		"input.tlpktdrop": "true",   // 文字列
		"overlay.tiles":   []any{1}, // 文字列でない要素
		"video.fps":       30.5,     // 整数でない
		"nosuch.key":      1.0,
		"input":           1.0, // 構造体そのもの
	} {
		if err := Set(&c, key, bad); err == nil {
			t.Errorf("%s に %v は入れられない", key, bad)
		}
	}
	if _, ok := Get(c, "nosuch.key"); ok {
		t.Error("未知のキーは ok=false")
	}
}

func TestSetDoesNotShareSlices(t *testing.T) {
	c := Default()
	src := []string{"a", "b"}
	if err := Set(&c, "overlay.tiles", src); err != nil {
		t.Fatal(err)
	}
	src[0] = "changed"
	if got, _ := Get(c, "overlay.tiles"); got.([]string)[0] != "a" {
		t.Error("配列を共有しない")
	}
}

// スキーマの全キーが、実在し、種類が型と合っていること。画面に出ない設定キーが無いことも確かめる。
func TestSchemaMatchesConfig(t *testing.T) {
	c := Default()
	seen := map[string]bool{}
	for _, f := range Schema() {
		v, ok := Get(c, f.Key)
		if !ok {
			t.Errorf("スキーマのキーが設定に無い: %s", f.Key)
			continue
		}
		seen[f.Key] = true
		switch f.Kind {
		case KindInt:
			if _, ok := v.(int); !ok {
				t.Errorf("%s: int のはず: %T", f.Key, v)
			}
		case KindFloat:
			if _, ok := v.(float64); !ok {
				t.Errorf("%s: float64 のはず: %T", f.Key, v)
			}
		case KindString, KindEnum:
			if _, ok := v.(string); !ok {
				t.Errorf("%s: string のはず: %T", f.Key, v)
			}
		case KindBool:
			if _, ok := v.(bool); !ok {
				t.Errorf("%s: bool のはず: %T", f.Key, v)
			}
		case KindArgs, KindTiles:
			if _, ok := v.([]string); !ok {
				t.Errorf("%s: []string のはず: %T", f.Key, v)
			}
		default:
			t.Errorf("%s: 未知の種類 %s", f.Key, f.Kind)
		}
		if f.Kind == KindEnum && len(f.Enum) == 0 {
			t.Errorf("%s: enum の選択肢が無い", f.Key)
		}
		if f.Derived != "" {
			if dv, ok := Get(c, f.Derived); !ok || reflect.TypeOf(dv).Kind() != reflect.Bool {
				t.Errorf("%s: derived のキーが真偽値でない: %s", f.Key, f.Derived)
			}
		}
		// 既定値は、そのキーの検証を通る(画面から既定値へ戻しても、エラーにならない)
		if err := c.Validate(); err != nil {
			t.Fatalf("既定値が検証を通らない: %v", err)
		}
	}
	// 設定の全キー(lock.enabled だけは、専用の「本番ロック」で扱うので画面の項目ではない)が画面にある
	var walk func(t reflect.Type, prefix string)
	walk = func(rt reflect.Type, prefix string) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			tag := strings.Split(f.Tag.Get("toml"), ",")[0]
			key := tag
			if prefix != "" {
				key = prefix + "." + tag
			}
			if f.Type.Kind() == reflect.Struct {
				walk(f.Type, key)
				continue
			}
			if key == "lock.enabled" {
				continue
			}
			if !seen[key] {
				t.Errorf("設定キーが画面のスキーマに無い: %s", key)
			}
		}
	}
	walk(reflect.TypeOf(Config{}), "")
}
