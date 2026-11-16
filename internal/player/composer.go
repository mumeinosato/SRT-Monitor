package player

import (
	"image"
	_ "image/jpeg" // 無信号画像は PNG が基本だが、JPEG も読めるようにする
	_ "image/png"
	"log/slog"
	"os"
	"time"

	"srtrx/internal/config"
)

// composer は、出力先(X11、フレームバッファ)に依らない、「いま画面に出すべき1枚」の組み立て。
// 最新のフレームを取り、無信号の表示を決め、タイル・バッジを合成する。
// 前回と同じ内容なら、何もしない(再描画のコストを払わない)。
type composer struct {
	rt   *Runtime
	rend *Renderer

	lastSeq uint64
	lastSig string
	lastNS  bool

	showOverlay bool // ホットキーで切り替える、タイル全体の表示

	nsPath string
	nsImg  image.Image
}

func newComposer(rt *Runtime, rend *Renderer) *composer {
	return &composer{rt: rt, rend: rend, showOverlay: true}
}

// invalidate は、次の render で、必ず描き直させる。
func (c *composer) invalidate() { c.lastSig = "" }

// render は buf(bgr0、w×h)へ、いま出すべき1枚を組み立てる。変わっていなければ false を返す(buf は変えない)。
// force や、まだ何も出していない(haveShown=false)ときは、必ず組み立てる。
func (c *composer) render(buf []byte, w, h int, force, haveShown bool) bool {
	now := time.Now()
	snap := c.rt.Model.Snapshot(now)
	ov := Overlay{Snap: snap, FrameAge: c.rt.FrameAge(now), Visible: c.showOverlay}
	ns := snap.ShowNoSignal()
	sig := ov.Signature()

	seq, changed := c.rt.Store.Take(buf, c.lastSeq, now)
	if !changed && sig == c.lastSig && ns == c.lastNS && !force && haveShown {
		return false
	}
	if !changed {
		// 同じフレームのまま、タイルだけ変わった(または再送): きれいなフレームを取り直す
		if _, ok := c.rt.Store.Take(buf, 0, now); !ok {
			FillBlack(buf)
		}
	}
	c.lastSeq, c.lastSig, c.lastNS = seq, sig, ns

	if ns {
		c.fillNoSignal(buf, w, h, snap.Cfg.Display)
	}
	c.rend.Compose(buf, w, h, ov)
	return true
}

func (c *composer) fillNoSignal(buf []byte, w, h int, d config.Display) {
	if d.NoSignal == "image" && d.NoSignalImage != "" {
		if c.nsPath != d.NoSignalImage {
			c.nsPath, c.nsImg = d.NoSignalImage, nil
			f, err := os.Open(d.NoSignalImage)
			if err != nil {
				slog.Error("無信号画像を開けない", "path", d.NoSignalImage, "err", err)
			} else {
				img, _, err := image.Decode(f) // PNG と JPEG
				f.Close()
				if err != nil {
					slog.Error("無信号画像を読めない", "path", d.NoSignalImage, "err", err)
				} else {
					c.nsImg = img
				}
			}
		}
		if c.nsImg != nil {
			CopyImageFit(buf, w, h, c.nsImg)
			return
		}
	}
	FillBlack(buf)
}
