package player

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Renderer は、タイル・バッジを、ソフトウェアで bgr0(B G R 未使用、1画素4バイト)のフレームへ合成する。
// GTK にも X11 にも依存しない(純Go)。文字は Go の内蔵フォント(英語のみ)。
//
// 見た目(色、角丸、上辺の色線)は GTK 版と揃える。スプライト(文字を描いた小さな画像)は、
// 内容が変わったときだけ作り直し、毎フレームは、その矩形の合成だけを行う。
type Renderer struct {
	mu      sync.Mutex
	fonts   *opentype.Font
	faces   map[float64]font.Face
	sprites map[string]*sprite
}

type sprite struct {
	img  *image.RGBA // アルファ乗算済み(Go の image.RGBA の規約)
	w, h int
}

// NewRenderer はフォントを読み込む。
func NewRenderer() (*Renderer, error) {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	return &Renderer{fonts: f, faces: map[float64]font.Face{}, sprites: map[string]*sprite{}}, nil
}

func (r *Renderer) face(px float64) font.Face {
	if f, ok := r.faces[px]; ok {
		return f
	}
	f, err := opentype.NewFace(r.fonts, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err) // 固定のフォントとサイズなので起こらない
	}
	r.faces[px] = f
	return f
}

// 色(タイルの色名 → 値と、上辺の色線)。
var tileColors = map[string]color.RGBA{
	ColorNeutral: {244, 246, 250, 255},
	ColorGreen:   {52, 211, 153, 255},
	ColorRed:     {248, 113, 113, 255},
	ColorOrange:  {251, 146, 60, 255},
	ColorBlue:    {96, 165, 250, 255},
	ColorGray:    {255, 255, 255, 115}, // 0.45
	ColorYellow:  {251, 191, 36, 255},
}

func accentOf(name string) color.RGBA {
	if name == ColorNeutral || name == ColorGray || name == "" {
		return color.RGBA{255, 255, 255, 46} // 0.18
	}
	return tileColors[name]
}

func colorOf(name string) color.RGBA {
	if c, ok := tileColors[name]; ok {
		return c
	}
	return tileColors[ColorNeutral]
}

// coverage は、角丸の矩形(0,0)-(w,h)・半径 rad の内側にある度合い(0..1)。ふちを1画素ぶんぼかす。
func coverage(x, y float64, w, h, rad float64) float64 {
	// 矩形の中心からの、角丸の符号付き距離
	cx, cy := w/2, h/2
	qx := math.Abs(x-cx) - (cx - rad)
	qy := math.Abs(y-cy) - (cy - rad)
	d := math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - rad
	return math.Min(1, math.Max(0, 0.5-d))
}

func textWidth(f font.Face, s string) int { return font.MeasureString(f, s).Ceil() }

// roundedBox は、角丸の背景・ふち・上辺の色線を持つ、空の(文字なしの)スプライト画像を作る。
func roundedBox(w, h int, rad float64, bg, border, accent color.RGBA, accentH int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	fw, fh := float64(w), float64(h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cov := coverage(float64(x)+0.5, float64(y)+0.5, fw, fh, rad)
			if cov <= 0 {
				continue
			}
			c := bg
			// ふち(内側へ1画素)
			if coverage(float64(x)+0.5, float64(y)+0.5, fw, fh, rad) < 1 ||
				coverage(float64(x)+0.5-1, float64(y)+0.5-1, fw-2, fh-2, rad-1) <= 0 {
				c = over(c, border)
			}
			if y < accentH && accent.A > 0 {
				c = over(c, accent)
			}
			// image.RGBA は、アルファ乗算済み。ここまでの c は直接のアルファなので、掛けてから書く
			a := float64(c.A) / 255 * cov
			i := y*img.Stride + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = uint8(float64(c.R)*a), uint8(float64(c.G)*a), uint8(float64(c.B)*a), uint8(a*255)
		}
	}
	return img
}

// over は src を dst の上に重ねた色(どちらも直接のアルファ)。
func over(dst, src color.RGBA) color.RGBA {
	sa := float64(src.A) / 255
	da := float64(dst.A) / 255
	oa := sa + da*(1-sa)
	if oa == 0 {
		return color.RGBA{}
	}
	mix := func(s, d uint8) uint8 { return uint8((float64(s)*sa + float64(d)*da*(1-sa)) / oa) }
	return color.RGBA{mix(src.R, dst.R), mix(src.G, dst.G), mix(src.B, dst.B), uint8(oa * 255)}
}

func drawText(img *image.RGBA, f font.Face, s string, x, baseline int, c color.RGBA) {
	d := &font.Drawer{Dst: img, Src: image.NewUniform(color.NRGBA{c.R, c.G, c.B, c.A}), Face: f, Dot: fixed.P(x, baseline)}
	d.DrawString(s)
}

// tileSprite は、タイル1枚のスプライト(キャッシュする)。
func (r *Renderer) tileSprite(t TileView, scale float64) *sprite {
	key := fmt.Sprintf("tile|%s|%s|%s|%.2f", t.Title, t.Value, t.Color, scale)
	if s, ok := r.sprites[key]; ok {
		return s
	}
	sz := func(v float64) float64 { return math.Round(v * scale) }
	ft, fv := r.face(math.Max(7, sz(10))), r.face(math.Max(10, sz(19)))
	padX, padY := int(sz(12)), int(sz(8))
	tw, vw := textWidth(ft, t.Title), textWidth(fv, t.Value)
	w := maxInt(maxInt(tw, vw)+2*padX, int(sz(104)))
	titleH, valueH := ft.Metrics().Ascent.Ceil(), fv.Metrics().Ascent.Ceil()
	h := padY + titleH + int(sz(4)) + valueH + fv.Metrics().Descent.Ceil() + padY
	img := roundedBox(w, h, sz(10), color.RGBA{14, 16, 21, 224}, color.RGBA{255, 255, 255, 23}, accentOf(t.Color), maxInt(2, int(sz(2))))
	drawText(img, ft, t.Title, (w-tw)/2, padY+titleH+int(sz(2)), color.RGBA{255, 255, 255, 140})
	drawText(img, fv, t.Value, (w-vw)/2, padY+titleH+int(sz(4))+valueH, colorOf(t.Color))
	s := &sprite{img: img, w: w, h: h}
	r.sprites[key] = s
	return s
}

// pillSprite は、バッジ(ピル型)のスプライト。
func (r *Renderer) pillSprite(text, colorName string, px float64, padX, padY int) *sprite {
	key := fmt.Sprintf("pill|%s|%s|%.0f|%d|%d", text, colorName, px, padX, padY)
	if s, ok := r.sprites[key]; ok {
		return s
	}
	f := r.face(px)
	tw := textWidth(f, text)
	asc, desc := f.Metrics().Ascent.Ceil(), f.Metrics().Descent.Ceil()
	w, h := tw+2*padX, asc+desc+2*padY
	img := roundedBox(w, h, float64(h)/2, color.RGBA{14, 16, 21, 217}, color.RGBA{255, 255, 255, 31}, color.RGBA{}, 0)
	c := colorOf(colorName)
	switch colorName {
	case ColorGray:
		c = color.RGBA{180, 186, 198, 255}
	}
	drawText(img, f, text, padX, padY+asc, c)
	s := &sprite{img: img, w: w, h: h}
	r.sprites[key] = s
	return s
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// blend は sprite(アルファ乗算済み)を、bgr0 のフレーム(幅w、高さh)の (x0,y0) へ、不透明度 opaq(0..255)で合成する。
// 出力 = src + dst × (1 − src のアルファ)(src と src のアルファに、不透明度を掛けてから)。
// フレームの外へはみ出す部分は切り捨てる。
func blend(dst []byte, w, h int, s *sprite, x0, y0 int, opaq uint32) {
	stride := w * 4
	for y := 0; y < s.h; y++ {
		dy := y0 + y
		if dy < 0 || dy >= h {
			continue
		}
		srow := s.img.Pix[y*s.img.Stride : y*s.img.Stride+s.w*4]
		for x := 0; x < s.w; x++ {
			dx := x0 + x
			if dx < 0 || dx >= w {
				continue
			}
			sa := uint32(srow[x*4+3])
			if sa == 0 {
				continue
			}
			ia := 255 - sa*opaq/255
			i := dy*stride + dx*4
			dst[i+0] = clamp8((uint32(srow[x*4+2])*opaq + uint32(dst[i+0])*ia + 127) / 255) // B
			dst[i+1] = clamp8((uint32(srow[x*4+1])*opaq + uint32(dst[i+1])*ia + 127) / 255) // G
			dst[i+2] = clamp8((uint32(srow[x*4+0])*opaq + uint32(dst[i+2])*ia + 127) / 255) // R
		}
	}
}

func clamp8(v uint32) byte {
	if v > 255 {
		return 255
	}
	return byte(v)
}

// Overlay は、1回の描画に要る入力(スナップショットと、最後のフレームからの経過)。
type Overlay struct {
	Snap       Snapshot
	FrameAge   time.Duration // まだ無ければ負
	Visible    bool          // ホットキーで切り替える、タイル全体の表示
	NoSignalBG bool          // 無信号の黒/画像のとき(バッジの位置などは変えない)
}

// Signature は、描画の内容が変わったかを調べる文字列。同じなら、再描画しなくてよい。
func (o Overlay) Signature() string {
	var b strings.Builder
	c := o.Snap.Cfg
	fmt.Fprintf(&b, "v%v|e%v|p%s|s%.2f|o%.2f|", o.Visible, c.Overlay.Enabled, c.Overlay.Position, c.Overlay.TileScale, c.Overlay.Opacity)
	if o.Visible && c.Overlay.Enabled {
		for _, t := range o.Snap.Tiles(o.FrameAge) {
			fmt.Fprintf(&b, "%s=%s/%s,", t.Title, t.Value, t.Color)
		}
	}
	sb := o.Snap.StateBadge(o.FrameAge)
	cb := o.Snap.ControlLostBadge()
	fmt.Fprintf(&b, "|%s/%s/%v|%s", sb.Text, sb.Color, sb.SW, cb.Text)
	return b.String()
}

// Compose は buf(bgr0、w×h)へ、タイル・バッジを合成する。
func (r *Renderer) Compose(buf []byte, w, h int, o Overlay) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cfg := o.Snap.Cfg
	scale := cfg.Overlay.TileScale
	if scale <= 0 {
		scale = 1
	}
	top := cfg.Overlay.Position != "bottom"
	tilesShown := o.Visible && cfg.Overlay.Enabled

	if tilesShown {
		tiles := o.Snap.Tiles(o.FrameAge)
		var sp []*sprite
		total, gap := 0, int(math.Round(6*scale))
		for _, t := range tiles {
			s := r.tileSprite(t, scale)
			sp = append(sp, s)
			total += s.w
		}
		if len(sp) > 0 {
			total += gap * (len(sp) - 1)
		}
		x := (w - total) / 2
		margin := int(math.Round(8 * scale))
		opaq := uint32(math.Round(cfg.Overlay.Opacity * 255))
		for _, s := range sp {
			y := margin
			if !top {
				y = h - s.h - margin
			}
			blend(buf, w, h, s, x, y, opaq)
			x += s.w + gap
		}
	}

	// 状態バッジ(下端中央)。タイルを下に出すときは、上へ逃がす
	bottomMargin := 18
	if tilesShown && !top {
		bottomMargin = 90
	}
	if b := o.Snap.StateBadge(o.FrameAge); b.Text != "" {
		s := r.pillSprite(b.Text, b.Color, 18, 18, 5)
		x, y := (w-s.w)/2, h-s.h-bottomMargin
		blend(buf, w, h, s, x, y, 255)
		if b.SW {
			sw := r.pillSprite("SW", ColorGray, 11, 9, 2)
			blend(buf, w, h, sw, x+s.w+6, y+(s.h-sw.h)/2, 255)
		}
	}
	// 制御切断(下端右)
	if cb := o.Snap.ControlLostBadge(); cb.Text != "" {
		s := r.pillSprite(cb.Text, cb.Color, 14, 14, 4)
		blend(buf, w, h, s, w-s.w-10, h-s.h-10, 255)
	}
}

// FillBlack は buf を黒で埋める(bgr0 なので、B G R を 0、未使用バイトも 0)。
func FillBlack(buf []byte) {
	for i := range buf {
		buf[i] = 0
	}
}

// CopyImageFit は img を、bgr0 の buf(w×h)へ、縦横比を保って収め、中央に置く(最近傍法)。残りは黒。
func CopyImageFit(buf []byte, w, h int, img image.Image) {
	FillBlack(buf)
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 || w == 0 || h == 0 {
		return
	}
	scale := math.Min(float64(w)/float64(b.Dx()), float64(h)/float64(b.Dy()))
	dw, dh := int(float64(b.Dx())*scale), int(float64(b.Dy())*scale)
	x0, y0 := (w-dw)/2, (h-dh)/2
	for y := 0; y < dh; y++ {
		sy := b.Min.Y + int(float64(y)/scale)
		for x := 0; x < dw; x++ {
			sx := b.Min.X + int(float64(x)/scale)
			c := color.RGBAModel.Convert(img.At(sx, sy)).(color.RGBA)
			i := ((y0+y)*w + x0 + x) * 4
			buf[i+0], buf[i+1], buf[i+2] = c.B, c.G, c.R
		}
	}
}
