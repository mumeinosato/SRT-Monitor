# srtrx — MediaMTX の同梱、ビルド、テスト
VERSION := $(shell cat internal/mediamtx/VERSION)
SHA256  := $(shell cat internal/mediamtx/SHA256)
ASSET   := internal/mediamtx/assets/mediamtx_linux_amd64
URL     := https://github.com/bluenviron/mediamtx/releases/download/v$(VERSION)/mediamtx_v$(VERSION)_linux_amd64.tar.gz

.PHONY: all fetch-mediamtx build build-gtk build-lite test vet install clean

all: build

# 公式バイナリを取得して展開し、SHA-256 が固定値と一致しなければ失敗させる。
fetch-mediamtx:
	@if [ -f $(ASSET) ] && echo "$(SHA256)  $(ASSET)" | sha256sum -c --quiet - 2>/dev/null; then \
		echo "mediamtx v$(VERSION): 取得済み"; \
	else \
		set -e; tmp=$$(mktemp -d); \
		echo "mediamtx v$(VERSION) を取得"; \
		curl -fsSL -o $$tmp/mtx.tar.gz $(URL); \
		tar -xzf $$tmp/mtx.tar.gz -C $$tmp mediamtx; \
		echo "$(SHA256)  $$tmp/mediamtx" | sha256sum -c --quiet - || { echo "SHA-256 が一致しない"; exit 1; }; \
		mkdir -p $(dir $(ASSET)); install -m 0755 $$tmp/mediamtx $(ASSET); \
	fi

# リリースが出す既定の版は、これまでどおり GTK 版(cgo、-tags gtk)。
# cgo なしの新しい構成(X11 + Web 画面)は、build-lite。受け入れ試験(docs/VERIFY.md)を通すまで、既定を入れ替えない。
# 埋め込むファイルが無いとビルドが失敗するので、先に fetch-mediamtx を呼ぶ。
build: build-gtk

build-gtk: fetch-mediamtx
	@mkdir -p bin
	go build -tags gtk -o bin/srtrx ./cmd/srtrx
	go build -tags gtk -o bin/srtrx-player ./cmd/srtrx-player

# cgo なし(GTK 不要)。GOARCH=arm GOARM=7 などを付けて、そのままクロスビルドできる。
build-lite: fetch-mediamtx
	@mkdir -p bin/lite
	CGO_ENABLED=0 go build -o bin/lite/srtrx ./cmd/srtrx
	CGO_ENABLED=0 go build -o bin/lite/srtrx-player ./cmd/srtrx-player

vet: fetch-mediamtx
	CGO_ENABLED=0 go vet ./...
	go vet -tags gtk ./...

test: fetch-mediamtx
	CGO_ENABLED=0 go test ./...

install: build
	install -d $(HOME)/.local/bin $(HOME)/.config/systemd/user
	install -m 0755 bin/srtrx bin/srtrx-player $(HOME)/.local/bin/
	install -m 0644 deploy/srtrx.service deploy/srtrx-player.service $(HOME)/.config/systemd/user/

clean:
	go clean
	@echo "bin/ は手で消す"
