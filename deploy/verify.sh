#!/usr/bin/env bash
#
# 目标机部署前自检：只读检查，不修改任何文件
#
#   ./verify.sh
#
set -uo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

ok()   { printf '  \033[32m✔\033[0m %s\n' "$*"; }
bad()  { printf '  \033[31m✘\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; }
FAIL=0

echo "== TMP 部署环境自检 =="

# 1. 架构
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ok "CPU 架构 $ARCH（与包内二进制匹配）" ;;
  *)            bad "CPU 架构 $ARCH ≠ x86_64，本包二进制无法运行" ;;
esac

# 2. 内核与发行版
ok "内核 $(uname -sr)"
if [ -f /etc/os-release ]; then
  # shellcheck disable=SC1091
  . /etc/os-release 2>/dev/null && ok "发行版 ${PRETTY_NAME:-unknown}"
fi

# 3. 二进制存在且为 Linux ELF
BIN=""
for c in "$DIR/bin/tmp" "$DIR/tmp-linux-amd64" "$DIR/tmp"; do [ -f "$c" ] && BIN="$c" && break; done
if [ -z "$BIN" ]; then
  bad "未找到二进制（bin/tmp 或 tmp-linux-amd64）"
else
  if [ -x "$BIN" ]; then ok "二进制存在且可执行：$BIN"; else
    warn "二进制存在但无执行权限，执行：chmod +x $BIN"
  fi
  magic="$(head -c 4 "$BIN" 2>/dev/null | od -An -tx1 | tr -d ' \n')"
  [ "$magic" = "7f454c46" ] && ok "ELF 头正确（7f454c46）" || bad "文件头不是 ELF（magic=$magic），可能传输损坏"
  if command -v file >/dev/null 2>&1; then
    file "$BIN" | sed 's/^/     /'
  fi
fi

# 4. 校验和
if [ -f SHA256SUMS ] && command -v sha256sum >/dev/null 2>&1; then
  if sha256sum -c SHA256SUMS >/dev/null 2>&1; then ok "SHA256SUMS 全部校验通过"; else
    bad "SHA256SUMS 校验失败（文件可能损坏或被篡改）"
  fi
else
  warn "缺少 SHA256SUMS 或 sha256sum，跳过校验"
fi

# 5. 运行时依赖
[ -x /bin/sh ] && ok "/bin/sh 可用（本地执行采集命令需要）" || bad "/bin/sh 缺失"
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  ok "systemd 可用（可用 install.sh 托管）"
else
  warn "未检测到 systemd，请用 start.sh 裸跑或 --no-systemd"
fi

# 6. 端口占用
PORT="$(grep -E '^TMP_ADDR=' tmp.env.example 2>/dev/null | sed 's/.*://' | tr -dc '0-9')"
PORT="${PORT:-8080}"
if command -v ss >/dev/null 2>&1; then
  if ss -lnt "sport = :$PORT" 2>/dev/null | grep -q LISTEN; then
    bad "端口 $PORT 已被占用（ss 检出）"
  else
    ok "端口 $PORT 空闲"
  fi
elif command -v netstat >/dev/null 2>&1; then
  if netstat -lnt 2>/dev/null | grep -q ":$PORT "; then bad "端口 $PORT 已被占用"; else ok "端口 $PORT 空闲"; fi
else
  warn "无 ss/netstat，跳过端口检查（可用 nc -z 127.0.0.1 $PORT 自查）"
fi

# 7. 磁盘与内存
avail_kb="$(df -Pk "$DIR" | awk 'NR==2{print $4}')"
if [ -n "${avail_kb:-}" ] && [ "$avail_kb" -gt 1048576 ]; then ok "可用磁盘 $((avail_kb/1024)) MB"
else warn "可用磁盘 $(( ${avail_kb:-0} /1024 )) MB（建议 ≥ 1 GB）"; fi

mem_mb="$(awk '/MemTotal/{printf "%d", $2/1024}' /proc/meminfo 2>/dev/null || echo 0)"
[ "${mem_mb:-0}" -ge 1024 ] && ok "内存 ${mem_mb} MB" || warn "内存 ${mem_mb} MB（建议 ≥ 1 GB）"

# 8. 本机 TiUP（仅提示接管时应填的 SSH 用户）
if [ -d "$HOME/.tiup/storage/cluster/clusters" ]; then
  ok "本机 $(id -un) 用户有 TiUP 元数据：接管时 SSH 用户填 $(id -un)"
elif [ -d /root/.tiup/storage/cluster/clusters ]; then
  warn "检测到 /root/.tiup 有集群元数据：接管时 SSH 用户填 root"
else
  ok "本机未发现 TiUP 元数据（接管远程中控机时无需本机安装 TiUP）"
fi

echo
if [ "$FAIL" -eq 0 ]; then
  printf '\033[32m自检通过，可以执行 sudo ./install.sh\033[0m\n'
  exit 0
fi
printf '\033[31m发现 %d 项阻塞问题，请先处理\033[0m\n' "$FAIL"
exit 1
