package player

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// フレームバッファ(/dev/fb0)バックエンド。コンソール起動(デスクトップ無し)の Raspberry Pi 向け。
// X11 が無い($DISPLAY が空)ときに使う。cgo なし。
//
// 注意: この環境(コンテナ)には実機のフレームバッファが無く、ビルドと、構造体の読み取り・
// 画素変換のユニットテストまでしか確かめていない。実機では docs/RASPBERRY_PI.md の手順で確認すること。
// キーボードは読まない(ホットキーは使えない)。全画面・モニター選択の設定は無視する(画面全体に出す)。

var (
	fbQuitCh   = make(chan struct{})
	fbQuitOnce sync.Once
)

// QuitFB はフレームバッファのバックエンドを終了させる。
func QuitFB() { fbQuitOnce.Do(func() { close(fbQuitCh) }) }

// FBPath は使うフレームバッファのデバイス(環境変数 SRTRX_FB で変えられる)。
func FBPath() string {
	if p := os.Getenv("SRTRX_FB"); p != "" {
		return p
	}
	return "/dev/fb0"
}

// HaveFB はフレームバッファのデバイスがあるか。
func HaveFB() bool {
	_, err := os.Stat(FBPath())
	return err == nil
}

const (
	fbioGetVScreenInfo = 0x4600
	fbioGetFScreenInfo = 0x4602
)

// fbInfo は、フレームバッファの形(fb_var_screeninfo と fb_fix_screeninfo から必要な分)。
type fbInfo struct {
	XRes, YRes                int
	BPP                       int
	RedOff, GreenOff, BlueOff int
	LineLength                int // 1行のバイト数
	MemLen                    int
}

func u32(b []byte, off int) int { return int(*(*uint32)(unsafe.Pointer(&b[off]))) }

// parseFBInfo は、ioctl で得た2つの構造体のバイト列から、必要な値を読む。
// fb_var_screeninfo(32ビットのフィールドが並ぶ): xres@0 yres@4 bits_per_pixel@24
// red.offset@32 green.offset@44 blue.offset@56。
// fb_fix_screeninfo: id[16]、smem_start(unsigned long、ポインタ幅)、smem_len(u32)、type、type_aux、visual、
// xpanstep/ypanstep/ywrapstep(各u16)、line_length(u32)。ポインタ幅で、後ろのオフセットが変わる。
func parseFBInfo(vs, fs []byte, ptrSize int) (fbInfo, error) {
	if len(vs) < 60 || len(fs) < 16+ptrSize+28 {
		return fbInfo{}, errors.New("フレームバッファの情報が短い")
	}
	in := fbInfo{
		XRes: u32(vs, 0), YRes: u32(vs, 4), BPP: u32(vs, 24),
		RedOff: u32(vs, 32), GreenOff: u32(vs, 44), BlueOff: u32(vs, 56),
	}
	base := 16 + ptrSize
	in.MemLen = u32(fs, base)
	in.LineLength = u32(fs, base+24)
	if in.XRes <= 0 || in.YRes <= 0 {
		return fbInfo{}, fmt.Errorf("解像度が不正: %dx%d", in.XRes, in.YRes)
	}
	if in.LineLength == 0 {
		in.LineLength = in.XRes * in.BPP / 8
	}
	return in, nil
}

// blitBGR0 は、bgr0(w×h、1画素4バイト)のフレームを、フレームバッファのメモリ(dst)の、中央へ書く。
// 画面より大きい分は切り捨て、余白は触らない(事前に黒で埋めておく)。32bpp と16bpp(RGB565)に対応する。
func blitBGR0(dst []byte, in fbInfo, src []byte, w, h int) error {
	if in.BPP != 32 && in.BPP != 16 {
		return fmt.Errorf("未対応の色深度: %d bpp(32 と 16 のみ)", in.BPP)
	}
	x0, y0 := (in.XRes-w)/2, (in.YRes-h)/2
	cw, ch := w, h
	sx, sy := 0, 0
	if x0 < 0 {
		sx, cw, x0 = -x0, in.XRes, 0
	}
	if y0 < 0 {
		sy, ch, y0 = -y0, in.YRes, 0
	}
	if x0+cw > in.XRes {
		cw = in.XRes - x0
	}
	if y0+ch > in.YRes {
		ch = in.YRes - y0
	}
	switch in.BPP {
	case 32:
		direct := in.RedOff == 16 && in.GreenOff == 8 && in.BlueOff == 0 // B G R X の並び(そのまま写せる)
		for y := 0; y < ch; y++ {
			so := ((sy+y)*w + sx) * 4
			do := (y0+y)*in.LineLength + x0*4
			if do+cw*4 > len(dst) {
				return errors.New("フレームバッファの範囲を超える")
			}
			if direct {
				copy(dst[do:do+cw*4], src[so:so+cw*4])
				continue
			}
			for x := 0; x < cw; x++ {
				b, g, r := src[so+x*4], src[so+x*4+1], src[so+x*4+2]
				px := uint32(r)<<uint(in.RedOff) | uint32(g)<<uint(in.GreenOff) | uint32(b)<<uint(in.BlueOff)
				dst[do+x*4], dst[do+x*4+1], dst[do+x*4+2], dst[do+x*4+3] = byte(px), byte(px>>8), byte(px>>16), byte(px>>24)
			}
		}
	case 16: // RGB565
		for y := 0; y < ch; y++ {
			so := ((sy+y)*w + sx) * 4
			do := (y0+y)*in.LineLength + x0*2
			if do+cw*2 > len(dst) {
				return errors.New("フレームバッファの範囲を超える")
			}
			for x := 0; x < cw; x++ {
				b, g, r := uint16(src[so+x*4]), uint16(src[so+x*4+1]), uint16(src[so+x*4+2])
				px := (r>>3)<<11 | (g>>2)<<5 | (b >> 3)
				dst[do+x*2], dst[do+x*2+1] = byte(px), byte(px>>8)
			}
		}
	}
	return nil
}

// RunFB はフレームバッファのプレイヤーを動かし、終了コードを返す。
func RunFB(rt *Runtime) int {
	path := FBPath()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		slog.Error("フレームバッファを開けない(video グループに入っているか)", "path", path, "err", err)
		return 1
	}
	defer f.Close()
	var vs [160]byte
	var fs [80]byte
	if err := ioctl(f.Fd(), fbioGetVScreenInfo, unsafe.Pointer(&vs[0])); err != nil {
		slog.Error("FBIOGET_VSCREENINFO に失敗", "err", err)
		return 1
	}
	if err := ioctl(f.Fd(), fbioGetFScreenInfo, unsafe.Pointer(&fs[0])); err != nil {
		slog.Error("FBIOGET_FSCREENINFO に失敗", "err", err)
		return 1
	}
	in, err := parseFBInfo(vs[:], fs[:], int(unsafe.Sizeof(uintptr(0))))
	if err != nil {
		slog.Error("フレームバッファの情報を読めない", "err", err)
		return 1
	}
	if in.BPP != 32 && in.BPP != 16 {
		slog.Error("未対応の色深度", "bpp", in.BPP)
		return 1
	}
	size := in.LineLength * in.YRes
	if in.MemLen > 0 && in.MemLen < size {
		size = in.MemLen
	}
	mem, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		slog.Error("フレームバッファを mmap できない", "err", err)
		return 1
	}
	defer unix.Munmap(mem)
	slog.Info("フレームバッファ", "path", path, "size", fmt.Sprintf("%dx%d", in.XRes, in.YRes), "bpp", in.BPP, "line", in.LineLength)

	rend, err := NewRenderer()
	if err != nil {
		slog.Error("フォントを読めない", "err", err)
		return 1
	}
	cmp := newComposer(rt, rend)
	for i := range mem { // 余白は黒
		mem[i] = 0
	}
	var buf []byte
	bw, bh := 0, 0
	haveShown := false
	tk := time.NewTicker(10 * time.Millisecond)
	defer tk.Stop()
	for {
		select {
		case <-fbQuitCh:
			return 0
		case <-tk.C:
			w, h := rt.Store.Size()
			if w <= 0 || h <= 0 {
				continue
			}
			if w != bw || h != bh {
				buf, bw, bh = make([]byte, w*h*4), w, h
				haveShown = false
				for i := range mem {
					mem[i] = 0
				}
			}
			if !cmp.render(buf, w, h, false, haveShown) {
				continue
			}
			if err := blitBGR0(mem, in, buf, w, h); err != nil {
				slog.Error("フレームバッファへ書けない", "err", err)
				return 1
			}
			haveShown = true
		}
	}
}

func ioctl(fd uintptr, req uintptr, arg unsafe.Pointer) error {
	_, _, e := unix.Syscall(unix.SYS_IOCTL, fd, req, uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}
