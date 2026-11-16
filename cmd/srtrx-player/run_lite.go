//go:build !gtk

package main

import "srtrx/internal/player"

// 既定(cgo なし)のプレイヤー: X11(MIT-SHM)。
func runPlayer(rt *player.Runtime) int { return player.RunX11(rt) }
func quitPlayer()                      { player.QuitX11() }
