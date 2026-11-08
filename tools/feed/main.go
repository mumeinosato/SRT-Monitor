// feed は開発用ツール。単色とカウンターのテストフレーム(rgb24)を、プレイヤーのフレームソケットへ送る。
// ffmpeg の代わりに、プレイヤーの表示(最後のフレームの保持、バッジ、オーバーレイ)を確かめるために使う。
//
//	go run ./tools/feed -sock $XDG_RUNTIME_DIR/srtrx/frame.sock -w 1920 -h 1080 -fps 30
//	go run ./tools/feed -n 90       # 90枚送って終了(ffmpegの終了を模す)
//	go run ./tools/feed -partial    # 最後に半端なフレームを送って終了(ffmpegの落下を模す)
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	def := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "srtrx", "frame.sock")
	sock := flag.String("sock", def, "フレームソケット")
	w := flag.Int("w", 1920, "幅")
	h := flag.Int("h", 1080, "高さ")
	fps := flag.Int("fps", 30, "送信FPS")
	n := flag.Int("n", 0, "送るフレーム数(0なら無限)")
	partial := flag.Bool("partial", false, "終了前に半端なフレームを送る")
	flag.Parse()

	conn, err := net.Dial("unix", *sock)
	if err != nil {
		log.Fatalf("接続できない: %v", err)
	}
	defer conn.Close()

	buf := make([]byte, *w**h*3)
	tick := time.NewTicker(time.Second / time.Duration(*fps))
	defer tick.Stop()
	for i := 0; *n == 0 || i < *n; i++ {
		<-tick.C
		render(buf, *w, *h, i)
		if _, err := conn.Write(buf); err != nil {
			log.Fatalf("書けない(プレイヤーが落ちた?): %v", err)
		}
	}
	if *partial {
		conn.Write(buf[:len(buf)/3])
	}
	fmt.Fprintln(os.Stderr, "終了")
}

// render は、色が変わる背景と、フレーム番号に合わせて進むバーを描く。
func render(buf []byte, w, h, n int) {
	r, g, b := byte(n*2), byte(255-n*2), byte(128+n)
	for i := 0; i < len(buf); i += 3 {
		buf[i], buf[i+1], buf[i+2] = r/4, g/4, b/4
	}
	// 進行バー(下の1/8)
	barY := h * 7 / 8
	x1 := (n * 16) % w
	for y := barY; y < h; y++ {
		for x := 0; x < x1; x++ {
			o := (y*w + x) * 3
			buf[o], buf[o+1], buf[o+2] = 255, 255, 255
		}
	}
	// フレーム番号を2進数のブロックで(下位16bit)
	bs := h / 32
	for bit := 0; bit < 16; bit++ {
		on := (n>>bit)&1 == 1
		for y := 0; y < bs; y++ {
			for x := 0; x < bs; x++ {
				px, py := 20+bit*(bs+4)+x, 20+y
				if px >= w || py >= h {
					continue
				}
				o := (py*w + px) * 3
				if on {
					buf[o], buf[o+1], buf[o+2] = 255, 255, 0
				} else {
					buf[o], buf[o+1], buf[o+2] = 40, 40, 40
				}
			}
		}
	}
}
