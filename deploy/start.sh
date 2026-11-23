#!/bin/sh
#
# 裸跑启动（无 systemd 环境：容器 / Alpine / 临时验证）
#
# 本脚本为 POSIX sh，Alpine 等无 bash 的精简系统也能直接执行。
#
#   ./start.sh                    # 前台运行，:8080
#   TMP_ADDR=:9000 ./start.sh     # 自定义端口
#   TMP_DATA_DIR=/data ./start.sh # 自定义数据目录（数据库默认也在其中）
#   ./start.sh > tmp.log 2>&1 &   # 后台运行，日志落盘
#
set -eu

DIR=$(cd "$(dirname "$0")" && pwd)
cd "$DIR" || exit 1

BIN="$DIR/bin/tmp"
[ -x "$BIN" ] || BIN="$DIR/tmp-linux-amd64"
[ -x "$BIN" ] || BIN="$DIR/tmp"
if [ ! -x "$BIN" ]; then
  echo "未找到可执行二进制（bin/tmp 或 tmp-linux-amd64）" >&2
  exit 1
fi

TMP_ADDR="${TMP_ADDR:-:8080}"
TMP_DATA_DIR="${TMP_DATA_DIR:-$DIR/data}"
export TMP_ADDR TMP_DATA_DIR

mkdir -p "$TMP_DATA_DIR"

echo "TMP 启动：addr=$TMP_ADDR data=$TMP_DATA_DIR"
exec "$BIN"
