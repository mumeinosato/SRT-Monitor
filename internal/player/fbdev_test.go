package player

import (
	"encoding/binary"
	"testing"
)

// fake は、ポインタ幅 ptr(4 か 8)の機械の ioctl が返す2つの構造体を作る。
func fakeFB(ptr, xres, yres, bpp, roff, goff, boff, lineLen, memLen int) (vs, fs []byte) {
	vs = make([]byte, 160)
	put := func(b []byte, off, v int) { binary.LittleEndian.PutUint32(b[off:], uint32(v)) }
	put(vs, 0, xres)
	put(vs, 4, yres)
	put(vs, 24, bpp)
	put(vs, 32, roff)
	put(vs, 44, goff)
	put(vs, 56, boff)
	fs = make([]byte, 80)
	copy(fs, "BCM2708 FB")
	base := 16 + ptr
	put(fs, base, memLen)
	put(fs, base+24, lineLen)
	return
}

func TestParseFBInfoBothPointerWidths(t *testing.T) {
	for _, ptr := range []int{4, 8} { // 32bit ARM(Pi の 32bit OS)と 64bit
		vs, fs := fakeFB(ptr, 1280, 720, 32, 16, 8, 0, 5120, 5120*720)
		in, err := parseFBInfo(vs, fs, ptr)
		if err != nil {
			t.Fatalf("ptr=%d: %v", ptr, err)
		}
		if in.XRes != 1280 || in.YRes != 720 || in.BPP != 32 || in.LineLength != 5120 || in.MemLen != 5120*720 ||
			in.RedOff != 16 || in.GreenOff != 8 || in.BlueOff != 0 {
			t.Errorf("ptr=%d: %+v", ptr, in)
		}
	}
	// line_length が 0 のときは、解像度から求める
	vs, fs := fakeFB(8, 800, 600, 16, 11, 5, 0, 0, 0)
	in, err := parseFBInfo(vs, fs, 8)
	if err != nil || in.LineLength != 1600 {
		t.Errorf("line_length=0: %+v %v", in, err)
	}
	// 異常な入力
	if _, err := parseFBInfo(make([]byte, 10), fs, 8); err == nil {
		t.Error("短い入力はエラー")
	}
	vs, _ = fakeFB(8, 0, 0, 32, 16, 8, 0, 0, 0)
	if _, err := parseFBInfo(vs, fs, 8); err == nil {
		t.Error("解像度が0ならエラー")
	}
}

func solid(w, h int, b, g, r byte) []byte {
	buf := make([]byte, w*h*4)
	for i := 0; i < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2] = b, g, r
	}
	return buf
}

func TestBlit32DirectCentered(t *testing.T) {
	in := fbInfo{XRes: 10, YRes: 8, BPP: 32, RedOff: 16, GreenOff: 8, BlueOff: 0, LineLength: 48} // 行に余白がある(48 > 40)
	dst := make([]byte, in.LineLength*in.YRes)
	if err := blitBGR0(dst, in, solid(4, 2, 10, 20, 30), 4, 2); err != nil {
		t.Fatal(err)
	}
	at := func(x, y int) [3]byte { o := y*in.LineLength + x*4; return [3]byte{dst[o], dst[o+1], dst[o+2]} }
	// 4×2 が、(3,3) から置かれる(中央)
	if at(3, 3) != [3]byte{10, 20, 30} || at(6, 4) != [3]byte{10, 20, 30} {
		t.Errorf("中央に置く: %v %v", at(3, 3), at(6, 4))
	}
	if at(2, 3) != [3]byte{} || at(7, 3) != [3]byte{} || at(3, 2) != [3]byte{} || at(3, 5) != [3]byte{} {
		t.Error("余白は触らない")
	}
	// 行の余白(パディング)に書かない
	for y := 0; y < in.YRes; y++ {
		for x := in.XRes * 4; x < in.LineLength; x++ {
			if dst[y*in.LineLength+x] != 0 {
				t.Fatalf("パディングに書いた: (%d,%d)", x, y)
			}
		}
	}
}

func TestBlit32SwizzlesWhenOrderDiffers(t *testing.T) {
	// R G B X の並び(red offset 0, blue offset 16)
	in := fbInfo{XRes: 2, YRes: 1, BPP: 32, RedOff: 0, GreenOff: 8, BlueOff: 16, LineLength: 8}
	dst := make([]byte, 8)
	if err := blitBGR0(dst, in, solid(2, 1, 1, 2, 3), 2, 1); err != nil { // B=1 G=2 R=3
		t.Fatal(err)
	}
	if dst[0] != 3 || dst[1] != 2 || dst[2] != 1 { // メモリ上は R G B
		t.Errorf("並びを入れ替える: %v", dst[:4])
	}
}

func TestBlit16RGB565(t *testing.T) {
	in := fbInfo{XRes: 2, YRes: 1, BPP: 16, LineLength: 4}
	dst := make([]byte, 4)
	// 白(255,255,255)→ 0xFFFF、赤(R=255)→ 0xF800
	src := []byte{255, 255, 255, 0, 0, 0, 255, 0}
	if err := blitBGR0(dst, in, src, 2, 1); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint16(dst[0:]); got != 0xFFFF {
		t.Errorf("白: %04x", got)
	}
	if got := binary.LittleEndian.Uint16(dst[2:]); got != 0xF800 {
		t.Errorf("赤: %04x", got)
	}
}

func TestBlitClipsOversizedFrame(t *testing.T) {
	// 映像(6×4)が、画面(4×2)より大きい: 中央を切り出す。範囲を超えて書かない
	in := fbInfo{XRes: 4, YRes: 2, BPP: 32, RedOff: 16, GreenOff: 8, BlueOff: 0, LineLength: 16}
	dst := make([]byte, 16*2)
	src := solid(6, 4, 1, 1, 1)
	// 中央の列(x=1..4)、行(y=1..2)を示すため、目印を置く
	mark := func(x, y int, v byte) { o := (y*6 + x) * 4; src[o], src[o+1], src[o+2] = v, v, v }
	mark(1, 1, 9)
	mark(4, 2, 7)
	if err := blitBGR0(dst, in, src, 6, 4); err != nil {
		t.Fatal(err)
	}
	if dst[0] != 9 { // 画面の(0,0)=映像の(1,1)
		t.Errorf("左上は映像の(1,1): %d", dst[0])
	}
	if dst[16+12] != 7 { // 画面の(3,1)=映像の(4,2)
		t.Errorf("右下は映像の(4,2): %d", dst[16+12])
	}
	// 小さすぎるメモリは、パニックせずエラー
	if err := blitBGR0(make([]byte, 4), in, src, 6, 4); err == nil {
		t.Error("範囲を超えるならエラー")
	}
	if err := blitBGR0(dst, fbInfo{XRes: 4, YRes: 2, BPP: 24, LineLength: 12}, src, 6, 4); err == nil {
		t.Error("未対応の色深度はエラー")
	}
}
