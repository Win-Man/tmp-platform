#!/usr/bin/env bash
#
# TMP 卸载脚本
#
#   sudo ./uninstall.sh              # 停止服务、移除 systemd 单元，保留 $PREFIX/data（数据库与密钥）
#   sudo ./uninstall.sh --purge      # 连同安装目录与数据一起删除（不可恢复，需二次确认）
#
set -euo pipefail

PREFIX="/opt/tmp"
SERVICE_NAME="tmp"
PURGE="0"

c_red()   { printf '\033[31m%s\033[0m\n' "$*"; }
c_green() { printf '\033[32m%s\033[0m\n' "$*"; }
c_yellow(){ printf '\033[33m%s\033[0m\n' "$*"; }
die()     { c_red "错误：$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) PREFIX="${2:?--prefix 需要参数}"; shift 2 ;;
    --purge)  PURGE="1"; shift ;;
    -h|--help) sed -n '2,8p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "未知参数：$1" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "请用 root 运行"

echo "==> 卸载 TMP（${PREFIX}）"

if command -v systemctl >/dev/null 2>&1; then
  systemctl stop "$SERVICE_NAME" 2>/dev/null || true
  systemctl disable "$SERVICE_NAME" 2>/dev/null || true
  rm -f "/etc/systemd/system/$SERVICE_NAME.service"
  systemctl daemon-reload 2>/dev/null || true
  echo "  已移除 systemd 服务 $SERVICE_NAME"
fi

rm -f "$PREFIX/bin/tmp"
echo "  已删除二进制"

if [ "$PURGE" = "1" ]; then
  if [ ! -d "$PREFIX" ]; then
    echo "  安装目录已不存在"
    exit 0
  fi
  c_red "⚠ 即将删除整个目录 ${PREFIX}（含数据库 tmp.db 与密钥 secret.key），此操作不可恢复！"
  printf '请输入 YES 确认：'
  read -r answer
  [ "$answer" = "YES" ] || die "已取消"
  rm -rf "$PREFIX"
  c_green "✔ 已彻底删除 $PREFIX"
  exit 0
fi

c_yellow "已保留数据目录 $PREFIX/data（数据库与密钥）。如需彻底删除，重新执行加 --purge。"
