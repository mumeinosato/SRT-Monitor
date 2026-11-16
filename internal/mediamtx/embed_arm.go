//go:build arm

package mediamtx

import _ "embed"

// GOARCH=arm は、armv7(Raspberry Pi 2/3/3B+/4 の 32bit OS)向けを同梱する。armv6(Pi Zero/1)は対象外。
//
//go:embed assets/mediamtx_linux_armv7
var binary []byte

//go:embed SHA256.armv7
var sha256File string

// Arch は同梱した MediaMTX の CPU(公式のリリース資産の名前)。
const Arch = "linux_armv7"
