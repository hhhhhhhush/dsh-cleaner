#!/usr/bin/env bash
# 交叉编译四个平台的可执行文件到 dist/
# 用法：./build.sh [版本号]
set -euo pipefail

# 版本号：优先用命令行参数，其次取最近的 git tag，都没有就按源码里的版本
VERSION="${1:-$(git describe --tags --abbrev=0 2>/dev/null || echo 0.1.0)}"
OUT=dist
rm -rf "$OUT"
mkdir -p "$OUT"

echo "构建版本：$VERSION"

# 1) 图标：缺失时重新绘制（改配色只需动 tools/mkicon/main.go 再跑一次）
if [ ! -f assets/icon.ico ]; then
  echo "生成图标…"
  go run ./tools/mkicon
fi

# 2) Windows 资源（图标 + 文件属性里的版本信息）
#    需要 go-winres：go install github.com/tc-hib/go-winres@latest
#    没装也能编译，只是 exe 没有图标 —— 仓库里已带生成好的 .syso
if command -v go-winres >/dev/null 2>&1; then
  echo "生成 Windows 资源…"
  go-winres make --arch amd64,386,arm64 --in winres/winres.json
else
  echo "提示：未安装 go-winres，沿用仓库中已生成的 rsrc_windows_*.syso"
fi

build() {
  local goos=$1 goarch=$2 name=$3
  local ldflags="-s -w -X main.version=$VERSION"
  # Windows 用 GUI 子系统，双击不弹控制台
  if [ "$goos" = "windows" ]; then
    ldflags="$ldflags -H windowsgui"
  fi
  echo "  → $name ($goos/$goarch)"
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -trimpath -ldflags "$ldflags" -o "$OUT/$name" .
}

build windows amd64 "dsh-cleaner-windows-amd64.exe"
build darwin  amd64 "dsh-cleaner-darwin-amd64"
build darwin  arm64 "dsh-cleaner-darwin-arm64"
build linux   amd64 "dsh-cleaner-linux-amd64"

echo
echo "完成："
ls -lh "$OUT" | awk 'NR>1 {printf "  %-38s %s\n", $9, $5}'
