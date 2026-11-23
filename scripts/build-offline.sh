#!/usr/bin/env bash
#
# 构建离线部署包（在联网/有工具链的构建机上执行）
#
#   ./scripts/build-offline.sh                 # 默认 1.0.0-mvp / linux-amd64
#   VERSION=1.0.1 ./scripts/build-offline.sh   # 指定版本号
#   GOARCH=arm64 ./scripts/build-offline.sh    # 交叉编译到 ARM64
#
# 产物：dist/tmp-<version>-linux-<arch>.tar.gz（内含二进制、systemd 单元、安装脚本、部署手册）
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

APP="tmp"
VERSION="${VERSION:-1.0.0-mvp}"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-amd64}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PKG_NAME="${APP}-${VERSION}-${GOOS}-${GOARCH}"
STAGE="$ROOT/dist/$PKG_NAME"

echo ">> 交叉编译 ${GOOS}/${GOARCH}（${VERSION} / ${COMMIT}）"

# 前端产物必须先存在（//go:embed）。缺失时自动构建。
if [ ! -f backend/web/dist/index.html ]; then
  echo ">> 未发现前端产物，执行前端构建"
  npm --prefix frontend run build
fi

mkdir -p dist
GOOS="$GOOS" GOARCH="$GOARCH" CGO_ENABLED=0 go build -C backend -trimpath \
  -ldflags "-s -w \
    -X 'github.com/tmp-platform/backend/internal/config.Version=$VERSION' \
    -X 'github.com/tmp-platform/backend/internal/config.BuildTime=$BUILD_TIME' \
    -X 'github.com/tmp-platform/backend/internal/config.Commit=$COMMIT'" \
  -o "$ROOT/dist/$APP-$GOOS-$GOARCH" ./cmd/tmp

echo ">> 组装离线包目录"
rm -rf "$STAGE"
mkdir -p "$STAGE/bin"
install -m 0755 "$ROOT/dist/$APP-$GOOS-$GOARCH" "$STAGE/bin/$APP"
install -m 0755 "$ROOT/deploy/install.sh"        "$STAGE/install.sh"
install -m 0755 "$ROOT/deploy/uninstall.sh"      "$STAGE/uninstall.sh"
install -m 0755 "$ROOT/deploy/start.sh"          "$STAGE/start.sh"
install -m 0755 "$ROOT/deploy/verify.sh"         "$STAGE/verify.sh"
install -m 0644 "$ROOT/deploy/tmp.service"       "$STAGE/tmp.service"
install -m 0644 "$ROOT/deploy/tmp.env.example"    "$STAGE/tmp.env.example"
if [ -f "$ROOT/deploy/README-DEPLOY.md" ]; then
  install -m 0644 "$ROOT/deploy/README-DEPLOY.md" "$STAGE/README-DEPLOY.md"
fi

echo ">> 计算校验和"
( cd "$STAGE" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS )

echo ">> 打包"
TARBALL="$ROOT/dist/$PKG_NAME.tar.gz"
tar -C "$ROOT/dist" -czf "$TARBALL" "$PKG_NAME"

echo ">> 完成：$TARBALL"
ls -lh "$TARBALL"
echo ""
echo "包内文件："
tar -tzf "$TARBALL" | sed 's/^/  /'
