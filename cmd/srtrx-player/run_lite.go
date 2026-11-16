//go:build !gtk

package main

import (
	"sync"

	"srtrx/internal/player"
)

var (
	quitCh   = make(chan struct{})
	quitOnce sync.Once
)

// 既定(cgo なし)のプレイヤー。いまは、フレームを受けるだけで描画しない(X11 描画は次の段階)。
func runPlayer(rt *player.Runtime) int {
	<-quitCh
	return 0
}

func quitPlayer() { quitOnce.Do(func() { close(quitCh) }) }
