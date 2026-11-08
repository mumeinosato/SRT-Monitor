package player

/*
#cgo pkg-config: gtk4
#include <gtk/gtk.h>
#include <stdint.h>

// srtrx_set_frame は rgb24 のフレームから GdkMemoryTexture を作り、GtkPicture へ渡す。
//
// フレームごとに6MB級のテクスチャを作るので、参照は自分で管理する:
// 画素データは g_bytes_new でCのメモリへコピーし、テクスチャが持つ。
// GtkPicture が最新のテクスチャを保持し、前のテクスチャは set_paintable で即座に解放される。
// (gotk4 のラッパ経由だと、解放が「Goのファイナライザ → メインループへ依頼」の経路になり、
// 毎秒30個の大きなオブジェクトでは追いつかず、数GBまで増えた。)
static void srtrx_set_frame(uintptr_t pic, const void *data, int w, int h) {
	gsize stride = (gsize)w * 3;
	GBytes *bytes = g_bytes_new(data, stride * (gsize)h);
	GdkTexture *tex = gdk_memory_texture_new(w, h, GDK_MEMORY_R8G8B8, bytes, stride);
	g_bytes_unref(bytes);
	gtk_picture_set_paintable(GTK_PICTURE((gpointer)pic), GDK_PAINTABLE(tex));
	g_object_unref(tex);
}
*/
import "C"

import "unsafe"

// setFrame は rgb24 のフレーム(幅w、高さh)を picture へ表示する。メインスレッドから呼ぶ。
// data は呼び出しの間だけ使う(Cへコピーされる)。
func setFrame(picture uintptr, data []byte, w, h int) {
	if len(data) < w*h*3 || w <= 0 || h <= 0 {
		return
	}
	C.srtrx_set_frame(C.uintptr_t(picture), unsafe.Pointer(&data[0]), C.int(w), C.int(h))
}
