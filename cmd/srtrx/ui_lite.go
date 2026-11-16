//go:build !gtk

package main

import (
	"sync"

	"srtrx/internal/controller"
	"srtrx/internal/logx"
)

var (
	quitCh   = make(chan struct{})
	quitOnce sync.Once
)

// 既定(cgo なし)の操作画面。いまは、終了を待つだけ(headless と同じ)。
func runUI(ctl *controller.Controller, logs *logx.Logs) int {
	<-quitCh
	return 0
}

func quitUI() { quitOnce.Do(func() { close(quitCh) }) }
