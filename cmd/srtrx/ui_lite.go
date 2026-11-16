//go:build !gtk

package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"srtrx/internal/controller"
	"srtrx/internal/logx"
	"srtrx/internal/webui"
)

// 既定(cgo なし)の操作画面は、ローカルの Web 画面。ブラウザで開く。
var (
	webAddr  = flag.String("web", webui.DefaultAddr, "操作画面(Web)の待ち受け host:port。同じ機械だけなら 127.0.0.1(既定)。他の機械から使うときは -web-token も付ける")
	webToken = flag.String("web-token", os.Getenv("SRTRX_WEB_TOKEN"), "操作画面のトークン(空なら認証なし。環境変数 SRTRX_WEB_TOKEN でも指定できる)")
	webOpen  = flag.Bool("open", false, "起動時に、既定のブラウザで操作画面を開く(xdg-open)")

	quitCh   = make(chan struct{})
	quitOnce sync.Once
)

func runUI(ctl *controller.Controller, logs *logx.Logs) int {
	srv := webui.New(ctl, logs, webui.Options{Addr: *webAddr, Token: *webToken})
	if err := srv.Start(); err != nil {
		// 操作画面が使えなくても、映像の経路は止めない(監督と統計はそのまま動く)
		slog.Error("操作画面(Web)を始められない。映像には影響しない", "addr", *webAddr, "err", err)
	} else {
		slog.Info("操作画面(Web)", "url", srv.URL())
		if host, _, err := net.SplitHostPort(*webAddr); err == nil && *webToken == "" {
			if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				slog.Warn("操作画面が、認証なしで、他の機械から見える待ち受けになっている。-web-token を付けること", "addr", *webAddr)
			}
		}
		if *webOpen {
			go func() { _ = exec.Command("xdg-open", srv.URL()).Start() }()
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			srv.Shutdown(ctx)
		}()
	}
	<-quitCh
	return 0
}

func quitUI() { quitOnce.Do(func() { close(quitCh) }) }
