//go:build gtk

package frame

// GTK 版のプレイヤーは、GdkMemoryTexture(R8G8B8)へ渡すので、rgb24 で受ける。
const (
	BytesPerPixel = 3
	FFmpegPixFmt  = "rgb24"
)
