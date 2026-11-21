#!/usr/bin/env bash
# TMP 冒烟脚本：以演示模式启动单二进制，跑通核心链路并对关键契约做**真断言**。
#
# 用法：bash scripts/smoke.sh（需先 make build）
#
# 设计约定：
#   1. 每条断言都真实校验「期望值 vs 实际值」，失败即以非 0 退出，并在 stderr 打印
#      期望/实际，绝不用 echo 文案假装通过；
#   2. 只断言稳定的契约（错误码、HTTP 状态、关键字段），不断言易变的展示文案；
#   3. 断言弱化（把期望值改成当前现象）视为缺陷，不允许。
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="$ROOT/bin/tmp"
PORT="${PORT:-18080}"
BASE="http://127.0.0.1:$PORT"
WORK="$(mktemp -d)"
LOG="$WORK/server.log"
BODY_FILE="$WORK/body"

PASS_COUNT=0
HTTP_CODE=""
BODY=""

cleanup() {
  if [ -n "${PID:-}" ]; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# 断言与工具函数
# ---------------------------------------------------------------------------

die() {
  echo ">> 冒烟中止：$*" >&2
  exit 1
}

# assert_eq <expected> <actual> <desc>
assert_eq() {
  local expected="$1" actual="$2" desc="$3"
  if [ "$expected" = "$actual" ]; then
    PASS_COUNT=$((PASS_COUNT + 1))
    echo "   [PASS] ${desc} -> ${actual}"
    return 0
  fi
  echo "   [FAIL] ${desc}：期望 [${expected}]，实际 [${actual}]" >&2
  echo ">> 冒烟失败，服务日志：${LOG}" >&2
  exit 1
}

# assert_contains <haystack> <needle> <desc>
assert_contains() {
  local haystack="$1" needle="$2" desc="$3"
  case "$haystack" in
    *"$needle"*)
      PASS_COUNT=$((PASS_COUNT + 1))
      echo "   [PASS] ${desc}（含 ${needle}）"
      return 0
      ;;
  esac
  echo "   [FAIL] ${desc}：响应未包含 [${needle}]，前 300 字符：" >&2
  printf '%s\n' "$(printf '%s' "$haystack" | head -c 300)" >&2
  echo ">> 冒烟失败，服务日志：${LOG}" >&2
  exit 1
}

# assert_nonempty <value> <desc>
assert_nonempty() {
  local value="$1" desc="$2"
  if [ -n "$value" ] && [ "$value" != "None" ] && [ "$value" != "PARSE_ERROR" ]; then
    PASS_COUNT=$((PASS_COUNT + 1))
    echo "   [PASS] ${desc}（非空）"
    return 0
  fi
  echo "   [FAIL] ${desc}：值为空或不可解析 [${value}]" >&2
  echo ">> 冒烟失败，服务日志：${LOG}" >&2
  exit 1
}

# req <method> <path> [authHeader] [jsonBody]
# 结果写入全局 HTTP_CODE / BODY。
req() {
  local method="$1" path="$2" auth="${3:-}" data="${4:-}"
  local args=(-sS --noproxy '*' -X "$method" -o "$BODY_FILE" -w '%{http_code}')
  [ -n "$auth" ] && args+=(-H "$auth")
  [ -n "$data" ] && args+=(-H 'Content-Type: application/json' -d "$data")
  HTTP_CODE="$(curl "${args[@]}" "${BASE}${path}")" || die "请求失败：${method} ${path}"
  BODY="$(cat "$BODY_FILE")"
}

# jget <json> <dotted.path>：取字段（列表用下标），失败打印 PARSE_ERROR / None。
jget() {
  printf '%s' "$1" | python3 -c '
import sys, json
try:
    d = json.load(sys.stdin)
    for k in sys.argv[1].split("."):
        if k == "":
            continue
        d = d[int(k)] if isinstance(d, list) else d[k]
    print("None" if d is None else d)
except Exception:
    print("PARSE_ERROR")
' "$2"
}

# ---------------------------------------------------------------------------
# 0. 前置检查与启动
# ---------------------------------------------------------------------------

if [ ! -x "$BIN" ]; then
  die "未找到 ${BIN}，请先执行 make build"
fi

echo ">> 启动服务（演示模式，端口 ${PORT}，数据目录 ${WORK}）"
TMP_MOCK=true TMP_ADDR=":$PORT" TMP_DB="$WORK/tmp.db" TMP_DATA_DIR="$WORK/data" "$BIN" >"$LOG" 2>&1 &
PID=$!

READY=false
for _ in $(seq 1 60); do
  if curl -fsS --noproxy '*' "$BASE/healthz" >/dev/null 2>&1; then
    READY=true
    break
  fi
  sleep 0.5
done
[ "$READY" = "true" ] || die "服务在 30s 内未就绪，日志：${LOG}"

echo ">> [1] 探针"
req GET "/healthz"
assert_eq "200" "$HTTP_CODE" "/healthz HTTP 状态"
assert_eq "0" "$(jget "$BODY" "code")" "/healthz 业务码"
req GET "/readyz"
assert_eq "200" "$HTTP_CODE" "/readyz HTTP 状态"

echo ">> [2] 未认证访问业务接口（应 401 / 40101）"
req GET "/api/v1/clusters"
assert_eq "401" "$HTTP_CODE" "未认证 /clusters HTTP 状态"
assert_eq "40101" "$(jget "$BODY" "code")" "未认证 /clusters 业务码"

echo ">> [3] 默认管理员登录（admin/admin123，首次登录须改密）"
req POST "/api/v1/auth/login" "" '{"username":"admin","password":"admin123"}'
assert_eq "200" "$HTTP_CODE" "登录 HTTP 状态"
assert_eq "0" "$(jget "$BODY" "code")" "登录业务码"
TOKEN="$(jget "$BODY" "data.token")"
assert_nonempty "$TOKEN" "登录返回 token"
assert_eq "True" "$(jget "$BODY" "data.user.mustChangePassword")" "首启管理员 mustChangePassword"
AUTH="Authorization: Bearer $TOKEN"

echo ">> [4] 强制改密守卫（改密前访问业务接口应 403 / 40302）"
req GET "/api/v1/clusters" "$AUTH"
assert_eq "403" "$HTTP_CODE" "改密前 /clusters HTTP 状态"
assert_eq "40302" "$(jget "$BODY" "code")" "改密前 /clusters 业务码"

echo ">> [5] 修改初始密码 admin123 -> Tmp@123456"
req PUT "/api/v1/auth/password" "$AUTH" '{"oldPassword":"admin123","newPassword":"Tmp@123456"}'
assert_eq "200" "$HTTP_CODE" "改密 HTTP 状态"
assert_eq "0" "$(jget "$BODY" "code")" "改密业务码"
TOKEN="$(jget "$BODY" "data.token")"
assert_nonempty "$TOKEN" "改密后重新签发的 token"
AUTH="Authorization: Bearer $TOKEN"

echo ">> [6] 改密后业务接口放行（应 code=0）"
req GET "/api/v1/clusters?page=1&pageSize=10" "$AUTH"
assert_eq "200" "$HTTP_CODE" "改密后 /clusters HTTP 状态"
assert_eq "0" "$(jget "$BODY" "code")" "改密后 /clusters 业务码"
assert_eq "3" "$(jget "$BODY" "data.total")" "演示集群数量"
# 列表默认按 created_at DESC, id DESC 排序，首条不稳定，故断言集合内容而非顺序。
assert_contains "$BODY" "mock-prod-a" "集群列表包含演示集群 mock-prod-a"

echo ">> [7] 总览与拓扑"
req GET "/api/v1/overview/summary" "$AUTH"
assert_eq "0" "$(jget "$BODY" "code")" "总览业务码"
assert_eq "3" "$(jget "$BODY" "data.clusterTotal")" "总览集群总数"
req GET "/api/v1/clusters/1/topology" "$AUTH"
assert_eq "0" "$(jget "$BODY" "code")" "拓扑业务码"
assert_nonempty "$(jget "$BODY" "data.groups")" "拓扑分组"

echo ">> [8] SPA 深链回退（应 200 text/html）"
req GET "/clusters"
assert_eq "200" "$HTTP_CODE" "深链 /clusters HTTP 状态"
req GET "/clusters/1/instances"
assert_eq "200" "$HTTP_CODE" "深链 /clusters/1/instances HTTP 状态"

echo ">> [8.5] 首页引用的构建产物必须真实可取（白屏回归守卫）"
# 白屏根因是 /assets/* 被当成接口 404，index.html 拿到了但 JS 拿不到。
# 因此必须逐个校验 index.html 里引用的资源：状态 200、内容非空、且不是 index.html 本身。
req GET "/"
assert_eq "200" "$HTTP_CODE" "首页 HTTP 状态"
assert_contains "$BODY" "<div id=\"app\">" "首页挂载点"
ASSET_PATHS="$(printf '%s' "$BODY" | grep -oE '(src|href)="/assets/[^"]+"' | sed 's/.*="//; s/"$//')"
assert_nonempty "$ASSET_PATHS" "首页引用的 assets 列表"
ASSET_COUNT=0
while IFS= read -r ap; do
  [ -z "$ap" ] && continue
  req GET "$ap"
  assert_eq "200" "$HTTP_CODE" "构建产物 ${ap} HTTP 状态"
  case "$BODY" in
    '<!DOCTYPE'*|'<!doctype'*)
      echo "   [FAIL] 构建产物 ${ap} 返回了 index.html（会导致白屏）" >&2
      echo ">> 冒烟失败，服务日志：${LOG}" >&2
      exit 1
      ;;
  esac
  ap_len=${#BODY}
  if [ "$ap_len" -lt 100 ]; then
    echo "   [FAIL] 构建产物 ${ap} 内容过短（${ap_len}B），疑似未嵌入二进制" >&2
    echo ">> 冒烟失败，服务日志：${LOG}" >&2
    exit 1
  fi
  PASS_COUNT=$((PASS_COUNT + 1))
  echo "   [PASS] 构建产物 ${ap} 内容长度 -> ${ap_len}B"
  ASSET_COUNT=$((ASSET_COUNT + 1))
done <<EOF_ASSETS
$ASSET_PATHS
EOF_ASSETS
if [ "$ASSET_COUNT" -lt 2 ]; then
  echo "   [FAIL] 首页引用的构建产物数量过少（${ASSET_COUNT}），疑似 index.html 未正确嵌入" >&2
  echo ">> 冒烟失败，服务日志：${LOG}" >&2
  exit 1
fi
PASS_COUNT=$((PASS_COUNT + 1))
echo "   [PASS] 首页引用构建产物数量 -> ${ASSET_COUNT}"

echo ">> [9] 缺失静态资源不得回退 index.html（应 404 / 40401）"
req GET "/assets/not-exist.js"
assert_eq "404" "$HTTP_CODE" "/assets/not-exist.js HTTP 状态"
assert_eq "40401" "$(jget "$BODY" "code")" "/assets/not-exist.js 业务码"

echo ">> [9.5] PD 地址自动发现（留空即识别，无需用户手填）"
req POST "/api/v1/clusters/discover-pd" "$AUTH" \
  '{"name":"mock-prod-a","tiupHost":"10.20.1.11","tiupUser":"tidb","sshPassword":"x","dbHost":"10.20.1.21","dbPort":4000,"dbUser":"root","dbPassword":"x","pdEndpoint":""}'
assert_eq "200" "$HTTP_CODE" "discover-pd HTTP 状态"
assert_eq "0" "$(jget "$BODY" "code")" "discover-pd 业务码"
assert_nonempty "$(jget "$BODY" "data.primary")" "discover-pd 主地址"
assert_contains "$(jget "$BODY" "data.primary")" "2379" "discover-pd 返回 PD 默认端口"

req POST "/api/v1/clusters/test-connection" "$AUTH" \
  '{"name":"mock-prod-a","tiupHost":"10.20.1.11","tiupUser":"tidb","sshPassword":"x","dbHost":"10.20.1.21","dbPort":4000,"dbUser":"root","dbPassword":"x","pdEndpoint":""}'
assert_eq "200" "$HTTP_CODE" "PD 留空连接测试 HTTP 状态"
assert_nonempty "$(jget "$BODY" "data.discovered.endpoints.0")" "连接测试自动发现的 PD 地址"
assert_eq "pd" "$(jget "$BODY" "data.results.2.scope")" "第三项探测结果为 PD 通道"
assert_contains "$(jget "$BODY" "data.results.2.message")" "成功" "自动发现后 PD 通道探测通过"

echo ">> [10] 接管缺少连接测试凭证（应 40001）"
req POST "/api/v1/clusters" "$AUTH" \
  '{"name":"smoke-no-token","tiupHost":"10.0.0.11","dbHost":"10.0.0.11","pdEndpoint":"http://10.0.0.11:2379"}'
assert_eq "400" "$HTTP_CODE" "无 testToken 接管 HTTP 状态"
assert_eq "40001" "$(jget "$BODY" "code")" "无 testToken 接管业务码"

echo ">> [11] 发起巡检并等待报告生成"
req POST "/api/v1/inspection/runs" "$AUTH" '{"clusterIds":[1],"templateId":1}'
assert_eq "202" "$HTTP_CODE" "发起巡检 HTTP 状态（异步受理）"
assert_eq "0" "$(jget "$BODY" "code")" "发起巡检业务码"
assert_nonempty "$(jget "$BODY" "data.results.0.runId")" "巡检 runId"

REPORT_ID="None"
REPORT_CLUSTER="None"
for _ in $(seq 1 40); do
  req GET "/api/v1/inspection/reports?page=1&pageSize=5" "$AUTH"
  REPORT_ID="$(jget "$BODY" "data.list.0.id")"
  REPORT_CLUSTER="$(jget "$BODY" "data.list.0.clusterName")"
  case "$REPORT_ID" in
    "None" | "PARSE_ERROR" | "") sleep 1 ;;
    *) break ;;
  esac
done
assert_nonempty "$REPORT_ID" "巡检报告 ID"
assert_nonempty "$REPORT_CLUSTER" "巡检报告集群名"

echo ">> [12] 巡检报告下载（Markdown，正文非空且含集群名）"
DL_CODE="$(curl -sS --noproxy '*' -o "$WORK/report.md" -w '%{http_code}' \
  "$BASE/api/v1/inspection/reports/${REPORT_ID}/download?format=md" -H "$AUTH")" \
  || die "下载巡检报告失败"
assert_eq "200" "$DL_CODE" "巡检报告下载 HTTP 状态"
DL_SIZE="$(wc -c <"$WORK/report.md" | tr -d ' ')"
[ "$DL_SIZE" -gt 0 ] || die "巡检报告下载为空（0 字节）"
PASS_COUNT=$((PASS_COUNT + 1))
echo "   [PASS] 巡检报告大小 -> ${DL_SIZE} 字节"
assert_contains "$(cat "$WORK/report.md")" "$REPORT_CLUSTER" "巡检报告正文包含集群名"

echo ">> [13] 任务中心"
req GET "/api/v1/jobs?page=1&pageSize=5" "$AUTH"
assert_eq "0" "$(jget "$BODY" "code")" "任务列表业务码"
assert_nonempty "$(jget "$BODY" "data.list.0.status")" "任务列表首条状态"

# 放在最后：本用例会把 admin 锁定 10 分钟，不影响前面的鉴权请求。
echo ">> [14] 登录失败锁定（连续 5 次错误密码，第 5 次应 42901）"
for i in 1 2 3 4; do
  req POST "/api/v1/auth/login" "" '{"username":"admin","password":"tmp-wrong-pass"}'
  assert_eq "40101" "$(jget "$BODY" "code")" "第 ${i} 次密码错误业务码"
done
req POST "/api/v1/auth/login" "" '{"username":"admin","password":"tmp-wrong-pass"}'
assert_eq "429" "$HTTP_CODE" "第 5 次密码错误 HTTP 状态"
assert_eq "42901" "$(jget "$BODY" "code")" "第 5 次密码错误触发账号锁定业务码"

echo ">> 冒烟完成：${PASS_COUNT} 项断言全部通过，日志：${LOG}"
