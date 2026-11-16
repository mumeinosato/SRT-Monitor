# srtrx — MediaMTX の同梱、ビルド、テスト
#
# CPU は GOARCH で選ぶ(amd64、arm=armv7、arm64)。例: make build-lite GOARCH=arm GOARM=7
VERSION := $(shell cat internal/mediamtx/VERSION)
GOARCH  ?= $(shell go env GOARCH)

ifeq ($(GOARCH),amd64)
  MTX_ARCH   := linux_amd64
  MTX_SUFFIX := amd64
else ifeq ($(GOARCH),arm)
  MTX_ARCH   := linux_armv7
  MTX_SUFFIX := armv7
else ifeq ($(GOARCH),arm64)
  MTX_ARCH   := linux_arm64
  MTX_SUFFIX := arm64
else
  $(error 未対応の GOARCH: $(GOARCH)(amd64、arm、arm64 のみ))
endif

SHA256 := $(shell cat internal/mediamtx/SHA256.$(MTX_SUFFIX))
ASSET  := internal/mediamtx/assets/mediamtx_$(MTX_ARCH)
URL    := https://github.com/bluenviron/mediamtx/releases/download/v$(VERSION)/mediamtx_v$(VERSION)_$(MTX_ARCH).tar.gz
LITE_OUT ?= bin/lite-$(GOARCH)

.PHONY: all fetch-mediamtx fetch-mediamtx-all build build-gtk build-lite test vet install clean

all: build

# 公式バイナリを取得して展開し、SHA-256 が固定値と一致しなければ失敗させる(取得のみ。実行はしない)。
fetch-mediamtx:
	@if [ -f $(ASSET) ] && echo "$(SHA256)  $(ASSET)" | sha256sum -c --quiet - 2>/dev/null; then \
		echo "mediamtx v$(VERSION) $(MTX_ARCH): 取得済み"; \
	else \
		set -e; tmp=$$(mktemp -d); \
		echo "mediamtx v$(VERSION) $(MTX_ARCH) を取得"; \
		curl -fsSL -o $$tmp/mtx.tar.gz $(URL); \
		tar -xzf $$tmp/mtx.tar.gz -C $$tmp mediamtx; \
		echo "$(SHA256)  $$tmp/mediamtx" | sha256sum -c --quiet - || { echo "SHA-256 が一致しない"; exit 1; }; \
		mkdir -p $(dir $(ASSET)); install -m 0755 $$tmp/mediamtx $(ASSET); \
	fi

# 3つの CPU 分をすべて取得する(リリースのビルド用)。
fetch-mediamtx-all:
	$(MAKE) fetch-mediamtx GOARCH=amd64
	$(MAKE) fetch-mediamtx GOARCH=arm
	$(MAKE) fetch-mediamtx GOARCH=arm64

# リリースが出す既定の版は、これまでどおり GTK 版(cgo、-tags gtk。amd64 のみ)。
# cgo なしの新しい構成(X11 + Web 画面)は、build-lite。受け入れ試験(docs/VERIFY.md)を通すまで、既定を入れ替えない。
# 埋め込むファイルが無いとビルドが失敗するので、先に fetch-mediamtx を呼ぶ。
build: build-gtk

build-gtk: fetch-mediamtx
	@mkdir -p bin
	go build -tags gtk -o bin/srtrx ./cmd/srtrx
	go build -tags gtk -o bin/srtrx-player ./cmd/srtrx-player

# cgo なし(GTK 不要)。クロスビルドできる(GOARCH=arm GOARM=7 など)。出力は $(LITE_OUT)/。
build-lite: fetch-mediamtx
	@mkdir -p $(LITE_OUT)
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -o $(LITE_OUT)/srtrx ./cmd/srtrx
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build -o $(LITE_OUT)/srtrx-player ./cmd/srtrx-player

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
