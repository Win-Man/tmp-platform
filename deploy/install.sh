#!/usr/bin/env bash
#
# TMP（TiDB 管理平台）离线一键安装脚本
#
# 用法：
#   sudo ./install.sh                          # 默认装到 /opt/tmp，端口 8080，用户 tmp
#   sudo ./install.sh --port 9000              # 自定义端口
#   sudo ./install.sh --prefix /data/tmp       # 自定义安装目录
#   sudo ./install.sh --user tiup              # 用已有用户运行（接管同机 TiUP 集群时常用）
#   sudo ./install.sh --no-systemd             # 只铺文件，不注册服务（容器/无 systemd 环境）
#
# 脚本只使用系统自带命令（bash/coreutils/systemctl），不依赖网络。
set -euo pipefail

PREFIX="/opt/tmp"
PORT="8080"
RUN_USER="tmp"
USE_SYSTEMD="1"
SERVICE_NAME="tmp"

c_red()   { printf '\033[31m%s\033[0m\n' "$*"; }
c_green() { printf '\033[32m%s\033[0m\n' "$*"; }
c_yellow(){ printf '\033[33m%s\033[0m\n' "$*"; }
info()    { printf '  %s\n' "$*"; }
die()     { c_red "错误：$*" >&2; exit 1; }

usage() {
  sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
  exit 0
}

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)     PREFIX="${2:?--prefix 需要参数}"; shift 2 ;;
    --port)       PORT="${2:?--port 需要参数}"; shift 2 ;;
    --user)       RUN_USER="${2:?--user 需要参数}"; shift 2 ;;
    --no-systemd) USE_SYSTEMD="0"; shift ;;
    -h|--help)    usage ;;
    *) die "未知参数：$1（--help 查看用法）" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "请用 root 运行（sudo ./install.sh）"

# ---------- 定位二进制与模板 ----------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_SRC=""
for cand in "$SCRIPT_DIR/bin/tmp" "$SCRIPT_DIR/tmp-linux-amd64" "$SCRIPT_DIR/tmp"; do
  if [ -f "$cand" ]; then BIN_SRC="$cand"; break; fi
done
[ -n "$BIN_SRC" ] || die "未找到二进制：请把 tmp 放在 $SCRIPT_DIR/bin/tmp 或 $SCRIPT_DIR/tmp-linux-amd64"

TPL_SERVICE="$SCRIPT_DIR/tmp.service"
TPL_ENV="$SCRIPT_DIR/tmp.env.example"
[ -f "$TPL_SERVICE" ] || die "缺少模板 $TPL_SERVICE"

# ---------- 架构预检 ----------
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) : ;;
  *) c_yellow "警告：当前架构 $ARCH 与本包（x86_64）不符，二进制可能无法运行" ;;
esac

echo "==> 安装 TMP 到 ${PREFIX}（端口 ${PORT}，运行用户 ${RUN_USER}）"

# ---------- 运行用户 ----------
if id "$RUN_USER" >/dev/null 2>&1; then
  info "用户 $RUN_USER 已存在，直接复用"
else
  info "创建系统用户 $RUN_USER"
  if command -v useradd >/dev/null 2>&1; then
    useradd --system --no-create-home --shell "$(command -v nologin || echo /sbin/nologin)" "$RUN_USER" 2>/dev/null \
      || useradd -r -s /sbin/nologin "$RUN_USER"
  else
    die "缺少 useradd，请手动创建用户 $RUN_USER 后重试"
  fi
fi

# ---------- 目录与二进制 ----------
mkdir -p "$PREFIX/bin" "$PREFIX/data"
install -m 0755 "$BIN_SRC" "$PREFIX/bin/tmp"
chown -R "$RUN_USER":"$RUN_USER" "$PREFIX/data"
chmod 0750 "$PREFIX/data"

# ---------- 环境变量文件（已存在则不覆盖，避免覆盖现场配置） ----------
if [ -f "$PREFIX/tmp.env" ]; then
  info "保留已有配置 $PREFIX/tmp.env"
else
  sed "s|__TMP_PREFIX__|$PREFIX|g" "$TPL_ENV" > "$PREFIX/tmp.env"
  # 端口落到 env（只在默认行上替换，避免误伤注释）
  sed -i "s|^TMP_ADDR=:8080|TMP_ADDR=:$PORT|" "$PREFIX/tmp.env"
  chmod 0640 "$PREFIX/tmp.env"
  chown root:"$RUN_USER" "$PREFIX/tmp.env"
  info "已生成配置 $PREFIX/tmp.env"
fi

# ---------- SELinux ----------
if command -v getenforce >/dev/null 2>&1 && [ "$(getenforce 2>/dev/null)" = "Enforcing" ]; then
  if command -v restorecon >/dev/null 2>&1; then
    restorecon -R "$PREFIX" 2>/dev/null || true
    info "已对 $PREFIX 执行 restorecon（SELinux Enforcing）"
  fi
fi

# ---------- systemd ----------
if [ "$USE_SYSTEMD" = "0" ]; then
  c_yellow "已跳过 systemd 注册。手动启动："
  echo "  sudo -u $RUN_USER $PREFIX/bin/tmp"
  exit 0
fi

if ! command -v systemctl >/dev/null 2>&1; then
  die "未检测到 systemd。请改用 --no-systemd，或手动运行 $PREFIX/bin/tmp"
fi

sed -e "s|__TMP_PREFIX__|$PREFIX|g" -e "s|__TMP_USER__|$RUN_USER|g" "$TPL_SERVICE" \
  > "/etc/systemd/system/$SERVICE_NAME.service"
info "已写入 /etc/systemd/system/$SERVICE_NAME.service"

systemctl daemon-reload
systemctl enable "$SERVICE_NAME" >/dev/null 2>&1 || true
systemctl restart "$SERVICE_NAME"

# ---------- 健康检查 ----------
echo "==> 等待服务就绪…"
READY="0"
for _ in $(seq 1 30); do
  if command -v curl >/dev/null 2>&1; then
    code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/readyz" 2>/dev/null || true)"
    [ "$code" = "200" ] && READY="1" && break
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O /dev/null "http://127.0.0.1:$PORT/readyz" 2>/dev/null && READY="1" && break
  else
    # 无 curl/wget：纯 bash 探测端口
    if (exec 3<>"/dev/tcp/127.0.0.1/$PORT") 2>/dev/null; then exec 3<&- 3>&-; READY="1"; break; fi
  fi
  sleep 1
done

echo
if [ "$READY" = "1" ]; then
  c_green "✔ 安装完成，服务已就绪"
else
  c_yellow "⚠ 服务未在 30s 内通过 /readyz 检查，请查看日志排错"
fi

IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -n "${IP:-}" ] || IP="<本机IP>"
cat <<EOF

  访问地址   http://$IP:$PORT
  初始账号   admin / admin123（首次登录会强制修改密码）
  安装目录   $PREFIX
  配置文件   $PREFIX/tmp.env
  数据目录   $PREFIX/data（含 tmp.db 与 secret.key）

  常用命令
    systemctl status  $SERVICE_NAME
    systemctl restart $SERVICE_NAME
    journalctl -u $SERVICE_NAME -f

EOF

if [ "$READY" != "1" ]; then
  echo "--- 最近 30 行日志 ---"
  journalctl -u "$SERVICE_NAME" -n 30 --no-pager || true
  exit 1
fi

c_yellow "重要：请立即备份 $PREFIX/data/secret.key —— 丢失后已录入的集群凭据无法解密。"
