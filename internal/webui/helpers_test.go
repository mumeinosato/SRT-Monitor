package webui

import (
	"context"
	"time"
)

// nil2 は、テストの後片付け用の、短い期限つきのコンテキスト。
func nil2() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = cancel
	return ctx
}
