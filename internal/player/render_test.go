package player

import (
	"image"
	"image/color"
	"testing"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/ipc"
)

func newTestRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func black(w, h int) []byte { return make([]byte, w*h*4) }

func nonBlack(buf []byte, w, x0, y0, x1, y1 int) int {
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			i := (y*w + x) * 4
			if buf[i] != 0 || buf[i+1] != 0 || buf[i+2] != 0 {
				n++
			}
		}
	}
	return n
}

func ovl(mut func(*Snapshot)) Overlay {
	s := snap(mut)
	return Overlay{Snap: s, FrameAge: -1, Visible: true}
}

func TestComposeDrawsTilesAtTopCenter(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 1280, 720
	buf := black(w, h)
	r.Compose(buf, w, h, ovl(nil))
	if n := nonBlack(buf, w, 0, 0, w, 100); n < 3000 {
		t.Fatalf("上端にタイルが描かれる: %d 画素", n)
	}
	if n := nonBlack(buf, w, 0, 150, w, h); n != 0 {
		t.Errorf("上端以外には何も描かない(running でバッジなし): %d 画素", n)
	}
	// 左右の端は空いている(中央寄せ)
	if n := nonBlack(buf, w, 0, 0, 100, 100); n != 0 {
		t.Errorf("左端は空き: %d", n)
	}
}

func TestComposeBottomPositionAndBadgeMovesUp(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 1280, 720
	buf := black(w, h)
	o := ovl(func(s *Snapshot) {
		s.Cfg.Overlay.Position = "bottom"
		s.State.State = ipc.StateBackoff
	})
	r.Compose(buf, w, h, o)
	if nonBlack(buf, w, 0, 0, w, 100) != 0 {
		t.Error("bottom のとき、上端は空き")
	}
	if nonBlack(buf, w, 0, h-80, w, h) == 0 {
		t.Error("bottom のとき、タイルは下端")
	}
	// バッジ(再接続中)は、タイルと重ならない高さ(下から約90px)に出る
	if nonBlack(buf, w, w/2-100, h-150, w/2+100, h-95) == 0 {
		t.Error("バッジがタイルの上に出る")
	}
}

func TestComposeBadgeColorsAndControlLost(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 1280, 720
	buf := black(w, h)
	r.Compose(buf, w, h, ovl(func(s *Snapshot) { s.State.State = ipc.StateBackoff; s.Cfg.Overlay.Enabled = false }))
	// 再接続中(橙)のバッジが、下端中央にある。橙の画素(R が大きく B が小さい)を探す
	orange := 0
	for y := h - 80; y < h; y++ {
		for x := w/2 - 150; x < w/2+150; x++ {
			i := (y*w + x) * 4
			if buf[i+2] > 200 && buf[i] < 120 {
				orange++
			}
		}
	}
	if orange < 30 {
		t.Errorf("橙の文字が出る: %d", orange)
	}
	// 制御切断は右下
	buf = black(w, h)
	r.Compose(buf, w, h, ovl(func(s *Snapshot) { s.ControlLost = true; s.Cfg.Overlay.Enabled = false }))
	if nonBlack(buf, w, w-200, h-60, w, h) == 0 {
		t.Error("制御切断が右下に出る")
	}
	if nonBlack(buf, w, 0, 0, w/2, h) != 0 {
		t.Error("左半分には出ない")
	}
}

func TestComposeHonorsVisibleAndEnabledAndShowBadge(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 1280, 720
	for name, o := range map[string]Overlay{
		"hotkey で非表示": func() Overlay { o := ovl(nil); o.Visible = false; return o }(),
		"overlay 無効":  ovl(func(s *Snapshot) { s.Cfg.Overlay.Enabled = false }),
	} {
		buf := black(w, h)
		r.Compose(buf, w, h, o)
		if n := nonBlack(buf, w, 0, 0, w, h); n != 0 {
			t.Errorf("%s: タイルを描いてはいけない(%d)", name, n)
		}
	}
	// show_state_badge=false で、バッジも制御切断も消える
	buf := black(w, h)
	r.Compose(buf, w, h, ovl(func(s *Snapshot) {
		s.Cfg.Overlay.Enabled = false
		s.Cfg.Display.ShowStateBadge = false
		s.State.State = ipc.StateBackoff
		s.ControlLost = true
	}))
	if nonBlack(buf, w, 0, 0, w, h) != 0 {
		t.Error("show_state_badge=false ですべて消す")
	}
}

func TestOpacityScalesAlpha(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 1280, 720
	bright := func(op float64) int {
		buf := make([]byte, w*h*4)
		for i := range buf {
			buf[i] = 255 // 白い背景
		}
		o := ovl(func(s *Snapshot) { s.Cfg.Overlay.Opacity = op })
		r.Compose(buf, w, h, o)
		// タイル中央付近の明るさ(背景が暗いタイルが、どれだけ白を暗くするか)
		sum := 0
		for y := 12; y < 40; y++ {
			for x := w/2 - 30; x < w/2+30; x++ {
				sum += int(buf[(y*w+x)*4+1])
			}
		}
		return sum
	}
	hi, lo := bright(1.0), bright(0.3)
	if !(lo > hi) {
		t.Errorf("不透明度が低いほど、背景(白)が透けて明るい: opacity1.0=%d opacity0.3=%d", hi, lo)
	}
}

func TestTileScaleChangesSize(t *testing.T) {
	r := newTestRenderer(t)
	tv := TileView{ID: "loss", Title: "LOSS", Value: "12", Color: ColorOrange}
	s1, s2 := r.tileSprite(tv, 1.0), r.tileSprite(tv, 2.0)
	if s2.w <= s1.w || s2.h <= s1.h {
		t.Errorf("tile_scale=2 は大きい: %dx%d → %dx%d", s1.w, s1.h, s2.w, s2.h)
	}
	if r.tileSprite(tv, 1.0) != s1 {
		t.Error("同じ内容はスプライトを使い回す")
	}
	// 値が長くなったら、幅が広がる
	long := r.tileSprite(TileView{ID: "rtt", Title: "RTT", Value: "123.45 ms", Color: ColorBlue}, 1.0)
	if long.w <= s1.w {
		t.Errorf("長い値で広がる: %d vs %d", long.w, s1.w)
	}
}

func TestComposeSafeOnTinyAndOffscreen(t *testing.T) {
	r := newTestRenderer(t)
	for _, sz := range [][2]int{{1, 1}, {10, 10}, {200, 50}, {320, 240}} {
		w, h := sz[0], sz[1]
		buf := black(w, h)
		r.Compose(buf, w, h, ovl(func(s *Snapshot) { s.State.State = ipc.StateStarting; s.ControlLost = true; s.State.HWFallback = true }))
	}
}

func TestSignatureChangesOnlyWhenContentChanges(t *testing.T) {
	a := ovl(nil)
	b := ovl(nil)
	if a.Signature() != b.Signature() {
		t.Error("同じ内容なら同じ署名")
	}
	c := ovl(func(s *Snapshot) { s.Stats.Loss = 5 })
	if a.Signature() == c.Signature() {
		t.Error("値が変われば署名が変わる")
	}
	d := ovl(func(s *Snapshot) { s.State.State = ipc.StateBackoff })
	if a.Signature() == d.Signature() {
		t.Error("状態バッジが変われば署名が変わる")
	}
	e := a
	e.Visible = false
	if a.Signature() == e.Signature() {
		t.Error("ホットキーで署名が変わる")
	}
	// 再接続中の秒数は、秒が変わると署名が変わる(バッジの文言が変わる)
	f1 := ovl(func(s *Snapshot) { s.State.State = ipc.StateBackoff })
	f1.FrameAge = 1200 * time.Millisecond
	f2 := f1
	f2.FrameAge = 2200 * time.Millisecond
	if f1.Signature() == f2.Signature() {
		t.Error("経過秒が変われば署名が変わる")
	}
	f3 := f1
	f3.FrameAge = 1500 * time.Millisecond
	if f1.Signature() != f3.Signature() {
		t.Error("同じ秒の中では署名が変わらない(毎フレーム再描画しない)")
	}
}

func TestCopyImageFitLetterboxes(t *testing.T) {
	// 横長の画像(2:1)を、正方形(100x100)へ。上下に黒帯が出る
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{200, 100, 50, 255})
		}
	}
	buf := make([]byte, 100*100*4)
	for i := range buf {
		buf[i] = 9
	}
	CopyImageFit(buf, 100, 100, img)
	at := func(x, y int) [3]byte { i := (y*100 + x) * 4; return [3]byte{buf[i], buf[i+1], buf[i+2]} }
	if at(50, 5) != [3]byte{0, 0, 0} || at(50, 95) != [3]byte{0, 0, 0} {
		t.Error("上下は黒帯(前の内容を残さない)")
	}
	if at(50, 50) != [3]byte{50, 100, 200} { // bgr0
		t.Errorf("中央に画像(B G R): %v", at(50, 50))
	}
}

func TestRenderWithDefaultConfigTiles(t *testing.T) {
	// 既定のタイル6枚が、設定の既定値(1.0倍)で、1280幅に収まる
	r := newTestRenderer(t)
	cfg := config.Default()
	total := 0
	for _, id := range cfg.Overlay.Tiles {
		total += r.tileSprite(TileView{ID: id, Title: "FRAME ERRORS", Value: "Connected", Color: ColorGreen}, 1.0).w + 6
	}
	if total > 1280 {
		t.Errorf("最悪の文言でも 1280px に収まる: %d", total)
	}
}
