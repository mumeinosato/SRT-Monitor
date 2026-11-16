//go:build !amd64 && !arm && !arm64

package mediamtx

// この CPU 向けの MediaMTX は同梱していない(ビルドは通るが、起動時に理由を出して止まる)。
var binary []byte

var sha256File string

// Arch は同梱した MediaMTX の CPU。
const Arch = ""
