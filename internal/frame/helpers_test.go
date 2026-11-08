package frame

import (
	"io"
	"os"
)

func openWrite(path string) (io.WriteCloser, error) {
	// 読み手がいない間は ENXIO になるので、呼び出し側で再試行する。
	return os.OpenFile(path, os.O_WRONLY|0x800 /* O_NONBLOCK */, 0)
}
