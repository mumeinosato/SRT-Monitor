// srtrx-player は、フレームの受信と全画面表示、オーバーレイの描画、ハートビートの送信を担う常駐プロセス。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"srtrx/internal/config"
	"srtrx/internal/logx"
	"srtrx/internal/player"
)

func main() { os.Exit(run()) }

func run() int {
	paths := config.DefaultPaths()
	logs := logx.Setup(paths.StateDir+"/srtrx-player.log", 500, slog.LevelInfo)
	defer logs.Close()
	defer logx.Recover("player.main")

	rt, _ := player.NewRuntime(paths)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := rt.Start(ctx); err != nil {
		slog.Error("プレイヤーを始められない", "err", err)
		return 1 // systemd が再起動する
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sig
		slog.Info("終了シグナルを受けた")
		cancel()
		time.AfterFunc(2*time.Second, func() { os.Exit(0) })
		player.Quit()
	}()

	slog.Info("プレイヤーを開始した", "pid", os.Getpid())
	return player.Run(rt)
}
