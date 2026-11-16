//go:build gtk

package main

import "srtrx/internal/player"

// GTK 版のプレイヤー(ビルドタグ gtk)。
func runPlayer(rt *player.Runtime) int { return player.Run(rt) }
func quitPlayer()                      { player.Quit() }
