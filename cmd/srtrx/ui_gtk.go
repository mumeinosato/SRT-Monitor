//go:build gtk

package main

import (
	"srtrx/internal/controller"
	"srtrx/internal/logx"
	"srtrx/internal/ui"
)

// GTK 版の操作画面(ビルドタグ gtk)。
func runUI(ctl *controller.Controller, logs *logx.Logs) int { return ui.Run(ctl, logs) }
func quitUI()                                               { ui.Quit() }
