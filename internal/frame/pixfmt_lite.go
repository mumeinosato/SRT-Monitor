//go:build !gtk

package frame

// 既定(cgo なし)のプレイヤーは、X11 の ZPixmap(24ビット深度、1画素4バイト、B G R 未使用)や
// フレームバッファへ、変換なしで書き込むので、bgr0 で受ける。
const (
	BytesPerPixel = 4
	FFmpegPixFmt  = "bgr0"
)
