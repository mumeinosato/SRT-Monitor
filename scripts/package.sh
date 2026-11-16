#!/bin/sh
# リリース用の tar.gz を作る。
#   scripts/package.sh <名前> <バイナリのディレクトリ> <出力のディレクトリ>
# 例: scripts/package.sh srtrx-v0.2.0-lite-linux-armv7 bin/lite-arm dist
#
# 中身: srtrx、srtrx-player、systemd のユニット、README、MediaMTX のライセンス。
# 出力: <出力>/<名前>.tar.gz と <名前>.tar.gz.sha256
set -eu

name=$1
bindir=$2
out=$3

[ -x "$bindir/srtrx" ] && [ -x "$bindir/srtrx-player" ] || { echo "$bindir に srtrx と srtrx-player が無い" >&2; exit 1; }

stage=$(mktemp -d)
trap 'rm -r "$stage"' EXIT
mkdir -p "$stage/$name" "$out"
cp "$bindir/srtrx" "$bindir/srtrx-player" "$stage/$name/"
cp deploy/srtrx.service deploy/srtrx-player.service "$stage/$name/"
cp README.md "$stage/$name/"
cp internal/mediamtx/LICENSE "$stage/$name/LICENSE-mediamtx"
tar -C "$stage" -czf "$out/$name.tar.gz" "$name"
(cd "$out" && sha256sum "$name.tar.gz" > "$name.tar.gz.sha256")
echo "作成: $out/$name.tar.gz"
