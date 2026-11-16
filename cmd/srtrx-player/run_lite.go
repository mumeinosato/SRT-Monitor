//go:build !gtk

package main

import (
	"log/slog"

	"srtrx/internal/player"
)

// 既定(cgo なし)のプレイヤー。$DISPLAY があれば X11(MIT-SHM)、無ければフレームバッファ(/dev/fb0)。
func runPlayer(rt *player.Runtime) int {
	switch {
	case player.HaveDisplay():
		slog.Info("出力先: X11")
		return player.RunX11(rt)
	case player.HaveFB():
		slog.Info("出力先: フレームバッファ", "path", player.FBPath())
		return player.RunFB(rt)
	default:
		slog.Error("出力先が無い: $DISPLAY(X11)も、フレームバッファ(/dev/fb0)も使えない")
		return 1 // systemd が再起動する(画面が使えるようになるまで)
	}
}

func quitPlayer() {
	player.QuitX11()
	player.QuitFB()
}
