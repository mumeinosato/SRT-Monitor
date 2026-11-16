package player

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/shm"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/sys/unix"

	"srtrx/internal/config"
)

// X11 バックエンド(cgo なし)。
//
// フレームは bgr0(1画素4バイト)で、MIT-SHM の共有メモリへ直接置いて X サーバへ渡す。
// 共有メモリは2面を交互に使う。タイル・バッジは Renderer が、その上へソフトウェアで合成する。
// 全画面は、WM に頼らず override-redirect の窓を、選んだモニターの矩形に置く(展示用の常駐を想定)。

var (
	x11QuitCh   = make(chan struct{})
	x11QuitOnce sync.Once
)

// QuitX11 は X11 バックエンドを終了させる(どのgoroutineからでもよい)。
func QuitX11() { x11QuitOnce.Do(func() { close(x11QuitCh) }) }

// HaveDisplay は X11 の表示先($DISPLAY)があるか。
func HaveDisplay() bool { return os.Getenv("DISPLAY") != "" }

type shmBuf struct {
	id  shm.Seg
	sys int // SysV の識別子
	mem []byte
}

type rect struct{ x, y, w, h int }

type x11 struct {
	rt   *Runtime
	conn *xgb.Conn
	scr  *xproto.ScreenInfo
	win  xproto.Window
	gc   xproto.Gcontext
	rend *Renderer

	bufs   [2]shmBuf
	cur    int
	vw, vh int // 映像の大きさ(共有メモリの面の大きさ)
	win0   rect

	cmp       *composer
	lastShown int // 最後に表示した面(Expose で再送する)
	haveShown bool

	hotkey  rune
	keysyms []xproto.Keysym
	perKey  int
	minKey  int

	display config.Display
	applied bool

	events chan any
}

type (
	evKey    struct{ code int }
	evExpose struct{}
	evClose  struct{}
)

// RunX11 は X11 のプレイヤーを動かし、終了コードを返す。
func RunX11(rt *Runtime) int {
	conn, err := xgb.NewConn()
	if err != nil {
		slog.Error("X サーバへ接続できない", "err", err, "display", os.Getenv("DISPLAY"))
		return 1
	}
	defer conn.Close()
	if err := shm.Init(conn); err != nil {
		slog.Error("MIT-SHM が使えない", "err", err)
		return 1
	}
	rend, err := NewRenderer()
	if err != nil {
		slog.Error("フォントを読めない", "err", err)
		return 1
	}
	setup := xproto.Setup(conn)
	x := &x11{rt: rt, conn: conn, scr: setup.DefaultScreen(conn), rend: rend, cmp: newComposer(rt, rend),
		events: make(chan any, 64), minKey: int(setup.MinKeycode)}
	if err := x.checkFormat(setup); err != nil {
		slog.Error("この X サーバの画素形式には対応していない", "err", err)
		return 1
	}
	if err := x.loadKeymap(setup); err != nil {
		slog.Warn("キーボードの対応表を読めない(ホットキーは使えない)", "err", err)
	}
	if err := x.createWindow(); err != nil {
		slog.Error("窓を作れない", "err", err)
		return 1
	}
	go x.eventLoop()
	return x.loop()
}

// checkFormat は、深度のピクスマップが 32bpp で、バイト順が LSB(B G R X の並び)かを確かめる。
func (x *x11) checkFormat(setup *xproto.SetupInfo) error {
	if setup.ImageByteOrder != xproto.ImageOrderLSBFirst {
		return errors.New("バイト順が MSB(未対応)")
	}
	for _, f := range setup.PixmapFormats {
		if f.Depth == x.scr.RootDepth {
			if f.BitsPerPixel != 32 {
				return fmt.Errorf("深度 %d が %d bpp(32 bpp のみ対応)", f.Depth, f.BitsPerPixel)
			}
			return nil
		}
	}
	return fmt.Errorf("深度 %d の形式が無い", x.scr.RootDepth)
}

func (x *x11) loadKeymap(setup *xproto.SetupInfo) error {
	n := int(setup.MaxKeycode) - int(setup.MinKeycode) + 1
	r, err := xproto.GetKeyboardMapping(x.conn, setup.MinKeycode, byte(n)).Reply()
	if err != nil {
		return err
	}
	x.keysyms, x.perKey = r.Keysyms, int(r.KeysymsPerKeycode)
	return nil
}

func (x *x11) atom(name string) xproto.Atom {
	r, err := xproto.InternAtom(x.conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0
	}
	return r.Atom
}

func (x *x11) setProp(prop xproto.Atom, typ xproto.Atom, format byte, data []byte) {
	n := uint32(len(data))
	if format == 32 {
		n /= 4
	} else if format == 16 {
		n /= 2
	}
	_ = xproto.ChangePropertyChecked(x.conn, xproto.PropModeReplace, x.win, prop, typ, format, n, data).Check()
}

func (x *x11) createWindow() error {
	win, err := xproto.NewWindowId(x.conn)
	if err != nil {
		return err
	}
	x.win = win
	// 最初は小さく作り、applyDisplay で、設定どおりの位置と大きさにする
	mask := uint32(xproto.CwBackPixel | xproto.CwEventMask)
	vals := []uint32{0, xproto.EventMaskKeyPress | xproto.EventMaskExposure | xproto.EventMaskStructureNotify}
	if err := xproto.CreateWindowChecked(x.conn, x.scr.RootDepth, win, x.scr.Root, 0, 0, 960, 540, 0,
		xproto.WindowClassInputOutput, x.scr.RootVisual, mask, vals).Check(); err != nil {
		return err
	}
	x.gc, err = xproto.NewGcontextId(x.conn)
	if err != nil {
		return err
	}
	if err := xproto.CreateGCChecked(x.conn, x.gc, xproto.Drawable(win), 0, nil).Check(); err != nil {
		return err
	}
	title := []byte("srtrx player")
	x.setProp(xproto.AtomWmName, xproto.AtomString, 8, title)
	x.setProp(x.atom("_NET_WM_NAME"), x.atom("UTF8_STRING"), 8, title)
	x.setProp(xproto.AtomWmClass, xproto.AtomString, 8, []byte("srtrx-player\x00srtrx-player\x00"))
	// ウィンドウの閉じる要求(WM_DELETE_WINDOW)を受ける
	if del := x.atom("WM_DELETE_WINDOW"); del != 0 {
		b := []byte{byte(del), byte(del >> 8), byte(del >> 16), byte(del >> 24)}
		x.setProp(x.atom("WM_PROTOCOLS"), xproto.AtomAtom, 32, b)
	}
	return nil
}

// ---- モニターと全画面 ----

// monitors は RandR のモニター(接続中の出力)を、名前から矩形への対応で返す。primary は主のモニター名。
func (x *x11) monitors() (rects map[string]rect, primary string) {
	rects = map[string]rect{}
	if err := randr.Init(x.conn); err != nil {
		return
	}
	res, err := randr.GetScreenResourcesCurrent(x.conn, x.scr.Root).Reply()
	if err != nil {
		return
	}
	var primOut randr.Output
	if p, err := randr.GetOutputPrimary(x.conn, x.scr.Root).Reply(); err == nil {
		primOut = p.Output
	}
	first := ""
	for _, o := range res.Outputs {
		info, err := randr.GetOutputInfo(x.conn, o, res.ConfigTimestamp).Reply()
		if err != nil || info.Crtc == 0 || info.Connection != randr.ConnectionConnected {
			continue
		}
		ci, err := randr.GetCrtcInfo(x.conn, info.Crtc, res.ConfigTimestamp).Reply()
		if err != nil || ci.Width == 0 {
			continue
		}
		name := string(info.Name)
		rects[name] = rect{int(ci.X), int(ci.Y), int(ci.Width), int(ci.Height)}
		if first == "" {
			first = name
		}
		if o == primOut {
			primary = name
		}
	}
	if primary == "" {
		primary = first
	}
	return
}

// targetRect は、設定の表示先(display.monitor)の矩形。見つからなければ画面全体。
func (x *x11) targetRect(d config.Display) rect {
	full := rect{0, 0, int(x.scr.WidthInPixels), int(x.scr.HeightInPixels)}
	rects, primary := x.monitors()
	name := d.Monitor
	if name == "" || name == "primary" {
		name = primary
	}
	if r, ok := rects[name]; ok {
		return r
	}
	if d.Monitor != "" && d.Monitor != "primary" {
		slog.Warn("指定のモニターが見つからないので、主のモニター(または画面全体)にする", "monitor", d.Monitor)
		if r, ok := rects[primary]; ok {
			return r
		}
	}
	return full
}

// applyDisplay は、表示するモニターと全画面の設定を窓へ反映する(変わったときだけ)。
func (x *x11) applyDisplay(d config.Display) {
	if x.applied && x.display == d {
		return
	}
	x.display, x.applied = d, true

	_ = xproto.UnmapWindowChecked(x.conn, x.win).Check()
	if d.Fullscreen {
		r := x.targetRect(d)
		x.win0 = r
		// WM に管理させず、選んだモニターの矩形にそのまま置く
		_ = xproto.ChangeWindowAttributesChecked(x.conn, x.win, xproto.CwOverrideRedirect, []uint32{1}).Check()
		_ = xproto.ConfigureWindowChecked(x.conn, x.win, xproto.ConfigWindowX|xproto.ConfigWindowY|xproto.ConfigWindowWidth|xproto.ConfigWindowHeight,
			[]uint32{uint32(r.x), uint32(r.y), uint32(r.w), uint32(r.h)}).Check()
		x.hideCursor()
	} else {
		// 窓表示(開発や確認用)は、映像の大きさ(画面に収まる範囲)にする
		ww, wh := x.rt.Store.Size()
		ww, wh = minInt(ww, int(x.scr.WidthInPixels)), minInt(wh, int(x.scr.HeightInPixels))
		x.win0 = rect{0, 0, ww, wh}
		_ = xproto.ChangeWindowAttributesChecked(x.conn, x.win, xproto.CwOverrideRedirect, []uint32{0}).Check()
		_ = xproto.ConfigureWindowChecked(x.conn, x.win, xproto.ConfigWindowWidth|xproto.ConfigWindowHeight, []uint32{uint32(ww), uint32(wh)}).Check()
		_ = xproto.ChangeWindowAttributesChecked(x.conn, x.win, xproto.CwCursor, []uint32{0}).Check() // 既定のカーソル
	}
	_ = xproto.MapWindowChecked(x.conn, x.win).Check()
	_ = xproto.ConfigureWindowChecked(x.conn, x.win, xproto.ConfigWindowStackMode, []uint32{xproto.StackModeAbove}).Check()
	if d.Fullscreen {
		// override-redirect の窓は、自動ではフォーカスを得ない(ホットキーに要る)
		_ = xproto.SetInputFocusChecked(x.conn, xproto.InputFocusParent, x.win, xproto.TimeCurrentTime).Check()
	}
	x.haveShown = false
	x.cmp.invalidate() // 再描画させる
}

// hideCursor は、窓の上のマウスカーソルを透明にする(展示では見せない)。
func (x *x11) hideCursor() {
	pm, err := xproto.NewPixmapId(x.conn)
	if err != nil {
		return
	}
	if xproto.CreatePixmapChecked(x.conn, 1, pm, xproto.Drawable(x.win), 1, 1).Check() != nil {
		return
	}
	defer xproto.FreePixmap(x.conn, pm)
	gc, err := xproto.NewGcontextId(x.conn)
	if err != nil {
		return
	}
	_ = xproto.CreateGCChecked(x.conn, gc, xproto.Drawable(pm), xproto.GcForeground, []uint32{0}).Check()
	_ = xproto.PolyFillRectangleChecked(x.conn, xproto.Drawable(pm), gc, []xproto.Rectangle{{X: 0, Y: 0, Width: 1, Height: 1}}).Check()
	xproto.FreeGC(x.conn, gc)
	cur, err := xproto.NewCursorId(x.conn)
	if err != nil {
		return
	}
	if xproto.CreateCursorChecked(x.conn, cur, pm, pm, 0, 0, 0, 0, 0, 0, 0, 0).Check() != nil {
		return
	}
	_ = xproto.ChangeWindowAttributesChecked(x.conn, x.win, xproto.CwCursor, []uint32{uint32(cur)}).Check()
}

// ---- 共有メモリ ----

func (x *x11) freeBufs() {
	for i := range x.bufs {
		b := &x.bufs[i]
		if b.mem == nil {
			continue
		}
		_ = shm.DetachChecked(x.conn, b.id).Check()
		_ = unix.SysvShmDetach(b.mem)
		b.mem = nil
	}
}

// ensureBufs は、映像の大きさに合う共有メモリの面を用意する(大きさが変わったら作り直す)。
func (x *x11) ensureBufs(w, h int) error {
	if x.bufs[0].mem != nil && x.vw == w && x.vh == h {
		return nil
	}
	x.freeBufs()
	size := w * h * 4
	for i := range x.bufs {
		id, err := unix.SysvShmGet(unix.IPC_PRIVATE, size, unix.IPC_CREAT|0o600)
		if err != nil {
			return fmt.Errorf("shmget: %w", err)
		}
		mem, err := unix.SysvShmAttach(id, 0, 0)
		if err != nil {
			return fmt.Errorf("shmat: %w", err)
		}
		seg, err := shm.NewSegId(x.conn)
		if err != nil {
			return err
		}
		if err := shm.AttachChecked(x.conn, seg, uint32(id), false).Check(); err != nil {
			return fmt.Errorf("X への共有メモリの登録: %w", err)
		}
		_, _ = unix.SysvShmCtl(id, unix.IPC_RMID, nil) // 登録が済んだら予約を消す(プロセス終了で解放される)
		x.bufs[i] = shmBuf{id: seg, sys: id, mem: mem}
	}
	x.vw, x.vh, x.cur = w, h, 0
	x.haveShown = false
	x.cmp.lastSeq = 0
	x.cmp.invalidate()
	return nil
}

// ---- 描画 ----

func (x *x11) draw(force bool) error {
	now := time.Now()
	snap := x.rt.Model.Snapshot(now)
	x.applyDisplay(snap.Cfg.Display)
	if want := firstRuneOf(snap.Cfg.Overlay.Hotkey); x.hotkey != want {
		x.hotkey = want
	}

	w, h := x.rt.Store.Size()
	if w <= 0 || h <= 0 {
		return nil
	}
	if err := x.ensureBufs(w, h); err != nil {
		return err
	}
	buf := x.bufs[x.cur].mem[:w*h*4]
	if !x.cmp.render(buf, w, h, force, x.haveShown) {
		return nil
	}
	return x.put(w, h)
}

// put は、いまの面を窓へ送る。映像が窓より小さければ、中央に置く(残りは黒)。
func (x *x11) put(w, h int) error {
	dx, dy := (x.win0.w-w)/2, (x.win0.h-h)/2
	if dx < 0 {
		dx = 0
	}
	if dy < 0 {
		dy = 0
	}
	pw, ph := w, h
	if pw > x.win0.w && x.win0.w > 0 { // 映像が窓より大きいときは、窓に収まる分だけ(左上から)
		pw = x.win0.w
	}
	if ph > x.win0.h && x.win0.h > 0 {
		ph = x.win0.h
	}
	shm.PutImage(x.conn, xproto.Drawable(x.win), x.gc, uint16(w), uint16(h), 0, 0, uint16(pw), uint16(ph),
		int16(dx), int16(dy), x.scr.RootDepth, xproto.ImageFormatZPixmap, 0, x.bufs[x.cur].id, 0)
	// サーバが読み終えるまで待つ(でないと、次のフレームを同じ面へ書いて、ちらつく)
	if _, err := xproto.GetInputFocus(x.conn).Reply(); err != nil {
		return err
	}
	x.lastShown, x.haveShown = x.cur, true
	x.cur ^= 1
	return nil
}

// ---- イベントとループ ----

func (x *x11) eventLoop() {
	defer func() {
		if v := recover(); v != nil {
			slog.Error("panic", "where", "x11.eventLoop", "value", v)
			os.Exit(2)
		}
	}()
	for {
		ev, err := x.conn.WaitForEvent()
		if ev == nil && err == nil { // 接続が閉じた
			x.events <- evClose{}
			return
		}
		switch e := ev.(type) {
		case xproto.KeyPressEvent:
			x.events <- evKey{int(e.Detail)}
		case xproto.ExposeEvent:
			x.events <- evExpose{}
		case xproto.DestroyNotifyEvent:
			x.events <- evClose{}
		case xproto.ClientMessageEvent:
			if len(e.Data.Data32) > 0 && xproto.Atom(e.Data.Data32[0]) == x.atom("WM_DELETE_WINDOW") {
				x.events <- evClose{}
			}
		}
	}
}

func firstRuneOf(s string) rune {
	for _, r := range s {
		return unicode.ToLower(r)
	}
	return 0
}

// keyMatches は、押されたキー(keycode)が、ホットキーの文字か。
func (x *x11) keyMatches(code int) bool {
	if x.hotkey == 0 || x.perKey == 0 {
		return false
	}
	base := (code - x.minKey) * x.perKey
	if base < 0 || base+x.perKey > len(x.keysyms) {
		return false
	}
	for i := 0; i < x.perKey && i < 2; i++ {
		ks := rune(x.keysyms[base+i])
		if ks < 0x80 && unicode.ToLower(ks) == x.hotkey { // ASCII の keysym は文字コードと同じ
			return true
		}
	}
	return false
}

func (x *x11) loop() int {
	defer x.freeBufs()
	tk := time.NewTicker(10 * time.Millisecond)
	defer tk.Stop()
	force := false
	for {
		select {
		case <-x11QuitCh:
			return 0
		case ev := <-x.events:
			switch e := ev.(type) {
			case evClose:
				slog.Info("窓が閉じられた")
				return 0
			case evExpose:
				force = true
			case evKey:
				if x.keyMatches(e.code) {
					x.cmp.showOverlay = !x.cmp.showOverlay
					x.cmp.invalidate()
				}
			}
		case <-tk.C:
			if err := x.draw(force); err != nil {
				slog.Error("描画に失敗した(X サーバとの接続が切れた?)", "err", err)
				return 1 // systemd が再起動する
			}
			force = false
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// 補助: 文字列の整形(ログ用)
func (r rect) String() string { return fmt.Sprintf("%dx%d+%d+%d", r.w, r.h, r.x, r.y) }

var _ = strings.TrimSpace
