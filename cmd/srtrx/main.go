// srtrx は制御プロセス。設定GUI、ffmpegとMediaMTXの監督、/metricsの取得と累計、プレイヤーへの送信を担う。
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/controller"
	"srtrx/internal/logx"
	"srtrx/internal/ui"
)

func main() { os.Exit(run()) }

func run() int {
	headless := flag.Bool("headless", false, "GUIを出さずに動かす(監督と統計のみ。受け入れテストや無人運用の確認用)")
	debug := flag.Bool("debug", false, "デバッグログを出す")
	flag.Parse()

	paths := config.DefaultPaths()
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	logs := logx.Setup(paths.LogFile, 1000, level)
	defer logs.Close()
	defer logx.Recover("srtrx.main")

	ctl, err := controller.New(controller.Options{Paths: paths, Logs: logs})
	if err != nil {
		slog.Error("制御を始められない", "err", err)
		fmt.Fprintln(os.Stderr, "制御を始められない:", err)
		return 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { defer close(runDone); ctl.Run(ctx) }()

	// SIGTERM のときは、先に ffmpeg(と MediaMTX)を止めてから終わる
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	stopAll := func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			slog.Error("子プロセスの停止が終わらない")
		}
	}

	slog.Info("制御を開始した", "pid", os.Getpid(), "headless", *headless)
	if *headless {
		<-sig
		slog.Info("終了シグナルを受けた")
		stopAll()
		return 0
	}

	go func() {
		<-sig
		slog.Info("終了シグナルを受けた")
		stopAll()
		ui.Quit()
		time.AfterFunc(3*time.Second, func() { os.Exit(0) })
	}()
	code := ui.Run(ctl, logs)
	stopAll()
	return code
}
