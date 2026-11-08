# srtrx — MediaMTX の同梱、ビルド、テスト
VERSION := $(shell cat internal/mediamtx/VERSION)
SHA256  := $(shell cat internal/mediamtx/SHA256)
ASSET   := internal/mediamtx/assets/mediamtx_linux_amd64
URL     := https://github.com/bluenviron/mediamtx/releases/download/v$(VERSION)/mediamtx_v$(VERSION)_linux_amd64.tar.gz

.PHONY: all fetch-mediamtx build test vet install clean

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

# 埋め込むファイルが無いとビルドが失敗するので、先に fetch-mediamtx を呼ぶ。
build: fetch-mediamtx
	@mkdir -p bin
	go build -o bin/srtrx ./cmd/srtrx
	go build -o bin/srtrx-player ./cmd/srtrx-player

vet: fetch-mediamtx
	go vet ./...

test: fetch-mediamtx
	go test ./...

install: build
	install -d $(HOME)/.local/bin $(HOME)/.config/systemd/user
	install -m 0755 bin/srtrx bin/srtrx-player $(HOME)/.local/bin/
	install -m 0644 deploy/srtrx.service deploy/srtrx-player.service $(HOME)/.config/systemd/user/

clean:
	go clean
	@echo "bin/ は手で消す"
