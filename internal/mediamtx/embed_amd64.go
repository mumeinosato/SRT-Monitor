//go:build amd64

package mediamtx

import _ "embed"

//go:embed assets/mediamtx_linux_amd64
var binary []byte

//go:embed SHA256.amd64
var sha256File string

// Arch は同梱した MediaMTX の CPU(公式のリリース資産の名前)。
const Arch = "linux_amd64"
