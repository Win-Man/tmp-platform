# TMP（TiDB 管理平台）测试报告

| 项目 | 内容 |
| --- | --- |
| 被测版本 | `1.0.0-mvp`（commit: unknown，本地工作区） |
| 测试执行 | QA 工程师 严过关（Edward） |
| 测试日期 | 2026-11-20 |
| 需求基线 | `docs/PRD.md`（F-01~F-71，P0 36 条） |
| 设计基线 | `docs/ARCHITECTURE.md` |
| 测试范围 | 后端 85 个 `.go` 文件、前端 14 个页面 + 9 个集群内 Tab、全部 REST 接口、SPA 静态回退 |
| 测试环境 | macOS（darwin），Go 1.22 + `CGO_ENABLED=0`，Node/npm（Vite 5），SQLite（`glebarez/sqlite`，WAL 模式） |
| 服务启动方式 | `TMP_MOCK=true TMP_DB=/tmp/qa-tmp.db TMP_ADDR=:18080`（演示模式）／`TMP_MOCK=false ... :18081`（真实模式） |
| 结论 | **第二轮回归通过（可交付 MVP）**：第一轮 11 项缺陷（P0 **0** / P1 **4** / P2 **7**）中 **10 项已修复并复验通过**、**1 项部分修复**（BUG-08 残留 2 处）；第二轮新发现 **0 项 P0/P1**、6 项 P2 观察项。详见 **§十 第二轮回归验收** |

---

## 一、测试结论摘要

| 指标 | 数值 |
| --- | --- |
| Go 单元测试函数 | **57 个**（含子用例 **78 个**） |
| Go 单测通过率 | **78 / 78 = 100%**（最终轮） |
| 接口冒烟断言 | **46 条**，全部通过 |
| 手工/脚本化核对项 | 18 项（构建、静态检查、敏感信息、日志、注入、并发、真实模式） |
| 缺陷总数 | **11 条**：P0 **0**、P1 **4**、P2 **7** |
| 是否可交付 | ✅ 可交付 MVP（第二轮回归后重新确认，见 §十） |

### 测试轮次记录（严格遵守 2 轮上限）

| 轮次 | 动作 | 结果 |
| --- | --- | --- |
| Round 1 | 编写 7 个 `_test.go`（57 个测试函数），首次运行 | 2 个失败，均为**测试自身断言缺陷**（见 §2.3），非源码缺陷 |
| Round 1 修正 | 自行修正断言（属"Send To: QA self"路径），进入 Round 2 | — |
| Round 2 | 全量回归 `go test ./... -count=1` + `-race` | **78/78 通过，0 失败，0 跳过** → 退出 |

> 未进入 Round 3。所有 P1/P2 缺陷均来自**接口冒烟与代码走查**，不属于单测断言失败项。

---

## 二、A 部分：构建与静态检查（独立重跑，未复用工程师结论）

### 2.1 后端编译

```bash
$ cd backend && CGO_ENABLED=0 go build ./...
BUILD_EXIT=0          # 无任何输出，编译通过
```

### 2.2 静态检查

```bash
$ cd backend && go vet ./...
VET_DONE              # 无输出 → 通过（包含全部 _test.go）

$ CGO_ENABLED=1 go test ./internal/repo/ ./internal/job/ -race -count=1
ok  	github.com/tmp-platform/backend/internal/repo	1.827s
ok  	github.com/tmp-platform/backend/internal/job	1.848s
# race detector 未报数据竞争
```

### 2.3 前端构建

```bash
$ cd frontend && rm -rf ../backend/web/dist && npm run build
../backend/web/dist/assets/ClusterDetailView-DHaxVU5x.js   37.57 kB │ gzip: 11.36 kB
../backend/web/dist/assets/element-BytzGmEj.js          1,097.15 kB │ gzip: 345.35 kB
✓ built in 7.06s

$ ls ../backend/web/dist
assets  index.html          # 产物正确落到 backend/web/dist（//go:embed 目标目录）
```

**补充：类型检查（`npm run build` 只做 Vite 打包，不含类型检查，故单独执行）**

```bash
$ cd frontend && npx vue-tsc --noEmit
TSC_EXIT=0
（无任何输出，error TS 命中 0 行）
```

**结论：TypeScript 类型检查 0 错误、通过。** `package.json` 已提供 `typecheck` 脚本，但 `build` 未串联它，CI 也未调用（见 R-07）。

### 2.4 CGO 依赖检查

```bash
$ grep -rn "mattn/go-sqlite3" backend/
（无结果）

$ grep -n "mattn" backend/go.mod
37:	github.com/mattn/go-isatty v0.0.20 // indirect
```

**结论：无 `mattn/go-sqlite3` 依赖**（`go-isatty` 是 gin 的间接依赖，与 SQLite/CGO 无关）。`CGO_ENABLED=0` 可直接编译，符合"单二进制、无 glibc 依赖"目标。

---

## 三、B 部分：Go 单元测试（本任务核心产出）

新增 7 个测试文件，全部按包就近放置：

| 测试文件 | 包 | 测试函数数 | 覆盖重点 |
| --- | --- | --- | --- |
| `internal/auth/auth_test.go` | auth | 13 | bcrypt 加解校验、salt 唯一性、密码策略、JWT 签发/解析/篡改/换密钥/过期/垃圾输入、**token_version 不匹配拒绝**、禁用账号、账号不存在、RBAC 矩阵、`Require` 40301 响应体 |
| `internal/collector/derive_test.go` | collector | 3（10 子用例） | `Derive` 健康状态推导四类场景 + PD-only 永不降级 + `ChannelFailed` |
| `internal/collector/mapping_test.go` | collector | 12 | 真实贴合 TiUP meta.yaml 样本（pd×3/tidb×2/tikv×3/tiflash/ticdc/grafana/prometheus/alertmanager）→ 组件/端口/主机/版本/角色/AZ 映射、CDC 去重、Dashboard/Prometheus/Grafana URL 推导、PD store 状态合并、架构归一化、PD 地址解析 |
| `internal/inspect/checkers_test.go` | inspect | 11 | 判定函数注册表（28 项）、CPU/磁盘/实例状态/水位/root@%/OOM/PD 副本/版本基线 **通过-警告-严重** 三态、**通过率公式（N/A 与 Unknown 不入分母）**、applies 版本不适用降级、结论判定 |
| `internal/job/recover_test.go` | job | 4 | **Recover 三件事**（running→interrupted、running 步骤→failed、collecting 全员归零、pending 重新入队）、幂等性、任务规格校验、未知类型拒绝 |
| `internal/service/job_state_test.go` | service | 5 | **任务状态机非法跃迁**（success/running/pending → 重试 40901；success/failed/canceled/interrupted → 取消 40901）、pending 取消、running 取消仅置标记、40401 |
| `internal/repo/tx_test.go` | repo | 6 | **写事务并发 8/16/32 goroutine**、8 并发 ×5 轮、非 busy 错误原样透传、busy 错误识别 |

### 3.1 关键断言示例（非空测试，均有真实断言）

```go
// token_version 不匹配必须拒绝（改密后旧 token 立即失效）
func TestAuthenticate_TokenVersionMismatch(t *testing.T) {
    u := &model.PlatformUser{ID: 5, Role: RoleAdmin, Status: "active", TokenVersion: 4}
    raw, _, _ := m.Issue(u.ID, u.Username, 3)   // 签发时 ver=3
    code, bizCode := callAuthenticated(t, NewMiddleware(m, &fakeUsers{user: u}), raw)
    if code != http.StatusUnauthorized || bizCode != 40101 { ... }
}

// 仅 PD 通道失败 → 不得降级
{"b) 仅 PD 通道失败→不得降级，仍 healthy",
  map[Kind]string{KindPD: "connection refused"}, 0, true, 3,
  HealthHealthy, HealthHealthy, 0},
```

### 3.2 完整测试输出

```bash
$ cd backend && CGO_ENABLED=0 go test ./... -count=1
?   	github.com/tmp-platform/backend/cmd/tmp	[no test files]
?   	github.com/tmp-platform/backend/internal/api	[no test files]
?   	github.com/tmp-platform/backend/internal/config	[no test files]
?   	github.com/tmp-platform/backend/internal/crypto	[no test files]
?   	github.com/tmp-platform/backend/internal/db	[no test files]
?   	github.com/tmp-platform/backend/internal/model	[no test files]
?   	github.com/tmp-platform/backend/internal/pkg/...	[no test files]
?   	github.com/tmp-platform/backend/web	[no test files]
ok  	github.com/tmp-platform/backend/internal/auth	2.492s
ok  	github.com/tmp-platform/backend/internal/collector	0.946s
ok  	github.com/tmp-platform/backend/internal/inspect	0.945s
ok  	github.com/tmp-platform/backend/internal/job	0.966s
ok  	github.com/tmp-platform/backend/internal/repo	0.881s
ok  	github.com/tmp-platform/backend/internal/service	0.818s
```

```
$ CGO_ENABLED=0 go test ./... -count=1 -v | grep -cE "^\s*--- PASS: "
78          # 含子用例；顶层 --- PASS 为 57；--- FAIL 0；--- SKIP 0
```

### 3.3 Round 1 中我自行修正的 2 处**测试缺陷**（非源码缺陷，如实记录）

| 断言 | 初版错误原因 | 修正 | 归属 |
| --- | --- | --- | --- |
| `TestJWT_Parse_ExpiredRejected` | 用 `ttl=-2s` 构造过期 token，但 `NewManager` 对 `ttl<=0` 兜底为 8h；改用 `ttl=1ms` 又因 JWT `exp` 以**秒**精度序列化而立即过期 | 改为 `ttl=1s` + `sleep 1.2s`，并注释说明精度原因 | 测试缺陷（Send To: QA self） |
| `TestNormalizeMeta_YAML` | 我把 `HostCount` 期望算成 10，漏算 `10.0.10.51` 同时承载 grafana/prometheus/alertmanager | 期望改为 11 并注明主机清单 | 测试缺陷（Send To: QA self） |

> 两条均为**测试侧断言错误**，源码行为正确，因此按规则路由到 QA 自查修复，未打扰工程师。

---

## 四、C 部分：接口冒烟（真实起服务，逐项 curl）

启动方式（全新库，未复用 `scripts/smoke.sh`）：

```bash
rm -f /tmp/qa-tmp.db* && mkdir -p /tmp/qa-data
TMP_MOCK=true TMP_DB=/tmp/qa-tmp.db TMP_ADDR=:18080 TMP_DATA_DIR=/tmp/qa-data ./bin/tmp
# 启动日志：tables=19 → 已生成主密钥 → 采集模式 mock=true → 任务引擎 4 workers
# → 已创建默认管理员 admin → 演示集群 3 个 → 监听 :18080
```

### 4.1 结果总表

| # | 验证项 | 期望 | 实测 | 结论 |
| --- | --- | --- | --- | --- |
| 1 | `GET /healthz` | 200 / code 0 | 200，`{"status":"ok","version":"1.0.0-mvp"}` | ✅ |
| 2 | `GET /readyz` | 200 / db up | 200，`{"db":"up","status":"ready"}` | ✅ |
| 3 | 未认证 `GET /api/v1/clusters` | 401 + 40101 | 401，`{"code":40101,"message":"缺少认证信息"}` | ✅ |
| 4 | 登录 `admin/admin123` | 返回 token，`mustChangePassword=true` | 一致，`mustChangePassword: true` | ✅ |
| 5 | 首登强制改密是否被**后端**强制 | 未改密前应禁止其他接口 | **未改密即 `GET /clusters` → 200**（见 BUG-01） | ❌ **P1** |
| 6 | 改密后旧 token 失效 | 401 + 40101 | 401，`{"code":40101,...}` | ✅ |
| 7 | 改密后新 token 可用 | 200 | 200，code 0 | ✅ |
| 8 | viewer `POST /clusters` | 403 + 40301 | 403，`{"code":40301}` | ✅ |
| 9 | viewer `DELETE /clusters/1` | 403 + 40301 | 403，`{"code":40301}` | ✅ |
| 10 | viewer `POST /inspection/runs` | 403 + 40301 | 403，`{"code":40301}` | ✅ |
| 11 | viewer `GET /users` | 403 + 40301 | 403，`{"code":40301}` | ✅ |
| 12 | viewer 读接口 `GET /clusters` | 200 | 200，code 0 | ✅ |
| 13 | operator `POST /users/1/reset-password` | 403 + 40301 | 403，`{"code":40301}` | ✅ |
| 14 | operator `PATCH /users/1/status` | 403 + 40301 | 403，`{"code":40301}` | ✅ |
| 15 | 连续 5 次错误密码 | 第 5 次 429 + 42901 | 429，`{"code":42901,"data":{"failedCount":5,"lockedUntil":"2026-11-20T23:49:48Z"}}` | ✅ |
| 16 | 第 6 次用**正确**密码 | 仍被锁 | 429，`{"code":42901,"data":{"remainSeconds":599}}` | ✅ |
| 17 | `/overview/summary` | 200 | 200，`clusterTotal:3, instanceTotal:31, hostTotal:10` | ✅ |
| 18 | `/clusters` | 200 | 200，3 个演示集群 | ✅ |
| 19 | `/clusters/1/topology` | 200 | 200，`groups` 按组件分组、含 role/version | ✅ |
| 20 | `/clusters/1/instances` | 200 | 200，5.3 KB | ✅ |
| 21 | `/clusters/1/tidb-users` | 200 | 200，用户含 resourceGroup | ✅ |
| 22 | `/clusters/1/variables`（PRD 前端路由写作 `/params`） | 200 | 200，含 `differs` 差异标记 | ✅（接口名 `/variables`，见说明） |
| 23 | `/clusters/1/sessions` | 200 | 200 | ✅ |
| 24 | 发起巡检（3 集群） | 202，返回 3 个 runId | 202，`IR20261120-0001~0003` | ✅ |
| 25 | 轮询巡检任务 | success | **poll#1 即 success**，`totalCount:28, passCount:25, warningCount:3, passRate:89.3, conclusion:risk` | ✅ |
| 26 | 报告生成 | status ready | ready，`fileSize 21378` | ✅ |
| 27 | `download?format=md` | `text/markdown` + attachment + 含集群名 | 200，`Content-Type: text/markdown; charset=utf-8`，`Content-Disposition: attachment; filename*=UTF-8''巡检报告_mock-test-c_...md`，12268 bytes，首行 `# TiDB 集群巡检报告` | ✅ |
| 28 | `download?format=html` | `text/html` + attachment + 含集群名 | 200，`text/html; charset=utf-8`，21378 bytes，含 `<title>巡检报告 RP20261120-0003 - mock-test-c</title>` | ✅ |
| 29 | 接管缺 name | 400 + 40001 | 400，`"集群名必填且不超过 64 个字符"` | ✅ |
| 30 | 接管缺 tiupHost | 400 + 40001 | 400，`"TiUP 主机地址必填"` | ✅ |
| 31 | 接管缺 dbHost | 400 + 40001 | 400，`"数据库地址必填"` | ✅ |
| 32 | 重复集群名 | 400 + 40001 | 400，`"集群名已存在（不区分大小写）"` | ✅ |
| 33 | 非法 IP `not-an-ip` | 400 + 40001 | **202 accepted，clusterId=4**（见 BUG-02） | ❌ **P1** |
| 34 | 端口越界 70000 | 400 + 40001 | 400，`"SSH 端口需在 1~65535 之间"` | ✅ |
| 35 | DB 端口越界 99999 | 400 + 40001 | 400，`"数据库端口需在 1~65535 之间"` | ✅ |
| 36 | 非法 PD 端点 `::::` | 400 + 40001 | **202 accepted，clusterId=5**（见 BUG-02） | ❌ **P1** |
| 37 | `test-connection` 不可达（mock 模式） | 15s 内 3 项独立结果 | 0s 返回 3 项（mock 全部模拟成功） | ✅（受 mock 限制，见 4.2） |
| 38 | `test-connection` 不可达（**真实模式**） | 15s 内 3 项独立结果 | **5.12s**：`ssh ok=false 5000ms`／`mysql ok=false 5000ms`／`pd ok=false 3002ms i/o timeout` | ✅ |
| 39 | SPA 深链 `/clusters/1/topology` | 200 text/html | 200，`text/html; charset=utf-8`，返回 `<div id="app">` | ✅（后端 fallback OK；前端路由见 BUG-06） |
| 40 | 不存在的静态资源 `/assets/not-exist-xxx.js` | 404 | **200 + index.html**（见 BUG-07） | ❌ **P2** |
| 41 | `clusters` 返回体凭据字段 | 无明文/掩码 | 命中敏感键：**无**；无 `dbPassword`/`sshPassword` | ✅ |
| 42 | 服务日志敏感串 | 无明文密码 | `QaPass1234` 0 次、`admin123` 0 次、`dbPassword` 0 次、`sshPassword` 0 次、`password_hash` 0 次 | ✅ |
| 43 | 审计导出敏感串 | 无明文密码 | 4161 字节 CSV，命中 5 类敏感串均为 **0 次** | ✅ |
| 44 | SQL 注入式集群名 `x'; DROP TABLE clusters;--` | 不得破坏数据 | 202 受理，集群表完好（GORM 参数化），但脏数据入库（见 BUG-09） | ⚠️ **P2** |
| 45 | `/jobs`、`/clusters/1/changes`、`/hosts`、`/roles/matrix` | 200 | 全部 200 | ✅ |
| 46 | 任务列表含本次巡检任务 | 有 8 条任务记录 | 200，job#8 `inspect/success` | ✅ |

### 4.2 冒烟过程中的说明

1. **mock 模式会掩盖 `test-connection` 的真实行为**：`TMP_MOCK=true` 下三通道固定返回"演示模式：模拟连接成功"，无法验证超时逻辑。因此我**额外以 `TMP_MOCK=false` 启动第二个实例（:18081）**，对 `10.255.255.1` 做真实探测，得到 5.12s、三项独立失败的结果，满足 PRD R-06「整体 15s 内、三项独立返回」。
2. **PRD 前端路由写 `/clusters/:id/params`，实际 API 为 `/clusters/:id/variables`**：PRD 第 128/308 行描述的是**前端路由**（页面路径），后端 API 命名不同不构成功能缺陷；前端 `ClusterVariablesTab` 调用的是 `/variables`，功能一致。仅记为命名差异，不计入缺陷。
3. 第一次执行冒烟时服务进程被外部回收（`Connection refused`），已改为受管后台任务重跑，所有结论来自第二次完整运行。

---

## 五、D 部分：两处已知风险的独立复核结论

### D-01 健康状态口径：`mock-test-c` 有实例 Down 但集群显示 healthy

**实测证据（同一时刻采集）：**

```
# 集群列表（GET /api/v1/clusters?keyword=mock-test-c）
{"name":"mock-test-c","instanceCount":8,"status":"healthy","collectStatus":"healthy",
 "collectFailStreak":0,"alertCount":0}

# 实例列表（GET /api/v1/clusters/3/instances）
实例数=8 非 up=1 → [('tidb','10.40.3.32',4001,'down')]
   ↑ 注意：Down 的是 TiDB 实例（:4001），不是工程师自述的 TiKV

# 同集群巡检报告（RP20261120-0003）
passRate=75 conclusion=fail critical=4 warning=3
  [critical] HA-04 实例运行状态 | 实际=1 个异常 | 期望=全部 up
  [critical] SYS-03 主机磁盘使用率 | 实际=最高 91.1% | 期望 < 85.0%
  [critical] CFG-02 OOM 行为配置 | 实际=log | 期望=cancel
  [critical] CAP-01 TiKV 存储水位 | 实际=88.0% | 期望 < 85.0%
```

**判定：**

1. **不违反 PRD 8.2 字面规则。** 8.2 的三级状态机（Healthy/Degraded/Error）触发条件全部是**「采集是否成功」**（"最近一次采集全部成功" / "部分采集项失败" / "连续 3 次采集失败"），与实例运行状态无关。本集群采集确实 100% 成功，`chanErrors` 为空 → `Derive` 返回 `healthy` 是**契约内的正确行为**，`Derive` 的单测（`TestDerive` 10 个子用例）也证明该函数严格按设计执行。
2. **但用户视角会产生明确误判。** 同一屏内出现"集群正常（绿色徽标）"与"巡检不通过 / 4 项严重 / 实例异常"两种相反信号，`alertCount` 还是 0。DBA 的默认心智是"绿色=可放心"，这与 PRD 8.2「异常集群数计入总览」的运营口径冲突——总览 `abnormalTotal: 0` 会漏掉这个实际存在故障节点的集群。
3. **建议（明确结论）：保持 `Derive` 与 `clusters.status` 语义不变，叠加「实例异常提示」。** 具体做法：
   - 后端在 `GET /clusters`、`GET /clusters/:id/overview` 增加 `abnormalInstanceCount`（`instances.status <> 'up'` 计数）与 `lastInspectionConclusion`；`alertCount` 保持告警通道口径不变。
   - 前端列表在「健康状态」旁加 `el-tag`：`实例异常 N`（danger 色，点击筛选到实例 Tab），并在总览增加"N 个集群存在实例异常"卡片。
   - **不建议**把实例 Down 直接映射为 `status=degraded`：那会污染 8.2 的采集降级语义（8.2 的 Degraded 还绑定「展示数据截至 HH:mm:ss + 失败项清单 + 重试按钮」，对实例 Down 场景是错误引导）。
   - 归类：**P2（体验/口径提示）**，非 P0/P1。

### D-02 SQLite 并发写：进程内互斥 + 5 次指数退避是否足够，是否应改 `BEGIN IMMEDIATE`

**实测数据：**

```bash
$ CGO_ENABLED=0 go test ./internal/repo/ -run TestTx_ConcurrentWrites -v -count=1
--- PASS: TestTx_ConcurrentWrites8           (0.01s)
--- PASS: TestTx_ConcurrentWrites16          (0.01s)
--- PASS: TestTx_ConcurrentWrites32          (0.02s)
--- PASS: TestTx_ConcurrentWrites8_Repeat    (5 轮 × 8 并发全部通过)
# 每个 goroutine 在事务内做「先读后写 + 删除 + 插入」，即 Tx() 注释要求的幂等形态，
# 这正是 deferred 事务的锁升级竞争路径 → 全程 0 次 SQLITE_BUSY 失败

$ CGO_ENABLED=1 go test ./internal/repo/ -race -count=1
ok  	github.com/tmp-platform/backend/internal/repo	1.827s      # 无 data race
```

**代码判断（`internal/repo/tx.go`）：**

- `writeMu sync.Mutex` 把**进程内所有写事务串行化**；`db.go` 又把连接池限制为 `MaxOpenConns(4)`，配合 DSN 的 `busy_timeout(5000)`。单进程场景下这三道保险叠加后，`SQLITE_BUSY` 实际不可能出现——压测结果与代码一致。
- 退避是**线性**而非指数（`attempt * 50ms` → 50/100/150/200ms，共 5 次），注释写"指数退避"与实现不符，仅属注释瑕疵。
- **真实缺口不在进程内，而在进程外**：`writeMu` 只保护本进程。若同一 `tmp.db` 被两个 TMP 实例（或运维用 `sqlite3` CLI）同时访问，deferred 事务的「读锁→写锁升级」会**立即**返回 `SQLITE_BUSY`，而 `busy_timeout` 对锁升级无效（SQLite 文档明确），此时 5 次退避也只能撞运气。
- 我验证了改造可行性（临时探针，已删除）：`glebarez/sqlite` **接受** `?_txlock=immediate` DSN 参数，打开后 `journal_mode=wal` 正常读写。

**明确结论：**

1. **当前实现对本项目使用场景（单二进制、单进程、单 db 文件）是充分且正确的**，8/16/32 并发压测 0 失败，不需为 MVP 阻塞。
2. **但建议改为 `BEGIN IMMEDIATE`**，理由：① 成本极低（`db.Open` 的 DSN 追加 `&_txlock=immediate`，一行改动，已验证可用）；② 一次性消除"先读后写"在跨进程/外部工具访问时的立即失败路径，属防御性收益；③ 语义更贴合 `Tx()` 的用途（写事务），可让 `writeMu` 从"正确性依赖"降级为"吞吐优化"，降低后续误删互斥锁的风险。
3. 若采纳 `_txlock=immediate`，**不要**同时删除 `writeMu`（WAL 下写写仍然互斥，保留可减少 busy 重试与 `busy_timeout` 等待带来的延迟抖动）；同时把注释里的"指数退避"改为"线性退避"。

---

## 六、E 部分：前端代码走查（读代码判断）

| 项 | 结论 | 证据 |
| --- | --- | --- |
| E-1 路由守卫是否拦截未登录与无权限 | ✅ 有效 | `router/index.ts:105-128`：`to.meta.public` 放行登录页；`!getToken()` → `{name:'login', query:{redirect: to.fullPath}}`；`ensureLoaded()` 失败则清态跳登录；`to.meta.action` 与 `auth.can(action)` 不匹配 → 回总览。12 个受保护路由**全部**声明了 `meta.action` |
| E-2 axios 拦截器 401 / 40101 / 40301 处理 | ✅ 正确 | `api/http.ts:58-87`：`status===401 \|\| code===40101` → 清 token + 触发 `onUnauthorized`（router 注册的"跳登录并带 redirect"）；403/40301 → 红条 toast；429/42901 → toast；40901 → 黄条；其余按 `X-TMP-Silent` 头决定是否 toast（避免批量请求刷屏） |
| E-3 列表页 loading / 空态 / 错误态三态 | ⚠️ **2/3 达标** | loading：14 个页面全部有 `v-loading` ✅；空态：关键列表通过 `el-table` 的 `empty-text` 提供可读空态（如 `ClusterListView` 的"暂无纳管集群，可点击右上角「接管集群」开始"）✅；**错误态缺失**：无任何页面有页内错误占位 + 重试按钮，且部分 `load()` 只有 `try/finally` 无 `catch`（`ClusterListView.load`、`ClusterDetailView`、`ClusterVariablesTab`），请求失败会产生 unhandled promise rejection，用户只看到一闪而过的全局 toast → **BUG-08** |
| E-4 低级问题（console.log / token 进 URL / 密码打日志） | ✅ 干净 | `grep -rn "console\.\(log\|debug\|info\)" src/` → **0 命中**；`grep -rn "token=" src/` → **0 命中**；报告下载走 `rawClient.get(url, {responseType:'blob'})` + `Authorization` 头（`api/index.ts:373`），未把 JWT 拼进 URL；token 仅存 `localStorage['tmp.token']`（XSS 可窃取，见遗留风险 R-02） |

**补充发现**：`ChangePasswordDialog` 在 `force` 模式下只隐藏了取消按钮、禁止点遮罩关闭，但**没有 `:show-close="false"`**，右上角 X 仍可关闭弹窗后继续使用平台 → 见 BUG-10。

---

## 七、缺陷清单

> 复现环境：`TMP_MOCK=true TMP_DB=/tmp/qa-tmp.db TMP_ADDR=:18080 ./bin/tmp`，全新库。

### P0（阻断：构建失败 / 核心流程不可用 / 安全漏洞）

**无。** 构建、`go vet`、前端打包全部通过；登录→接管→采集→巡检→报告下载主链路可跑通；未发现明文凭据泄漏、SQL 注入或越权。

### P1（功能不符 PRD 验收要点）

#### BUG-01（P1）默认弱口令的「强制改密」仅前端拦截，后端未强制

- **复现**：全新库启动 → `POST /auth/login {"username":"admin","password":"admin123"}`（返回 `mustChangePassword: true`）→ 直接用该 token 调 `GET /api/v1/clusters`
- **期望**：未完成首次改密前，非白名单接口应返回 40301/40901（PRD F-07 折中方案 A1 的全部安全性都建立在"强制改密"上）
- **实际**：`HTTP 200 {"code":0, ...}`，可正常读集群列表；进一步验证 `POST /users`、`POST /clusters` 等同理可用
- **证据**：
  ```
  =============== 3b. must_change_password=true 时是否强制改密 ===============
  HTTP=200 body={"code":0,"message":"ok","data":{"list":[{"id":3,"name":"mock-test-c",...
  ```
- **涉及文件**：`backend/internal/api/router.go`（`authed` 组无 must_change 门禁）、`backend/internal/auth/middleware.go:38`、`backend/internal/service/auth_service.go:262`（`EnsureDefaultAdmin` 硬编码 `DefaultAdminPassword`）
- **说明**：`docs/ARCHITECTURE.md:1295` A1 已记录"PRD F-07 要求不硬编码弱密码 vs 约束要求 admin/admin123"的冲突并选择后者。既然选了弱口令，**强制改密就必须是硬约束**，否则折中方案失效。
- **修复建议**：在 `Authenticate()` 后加一个 `RequirePasswordChanged()` 中间件，允许 `/auth/me`、`/auth/password`、`/auth/logout`，其余返回 40301（message: "首次登录请先修改初始密码"）。约 30 行。

#### BUG-02（P1）接管接口缺 IP / PD 端点格式校验，非法地址被受理并生成采集任务

- **复现（两种模式均可）**：
  ```bash
  # mock 模式 18080
  POST /api/v1/clusters {"name":"qa-x3","tiupHost":"not-an-ip","dbHost":"10.1.1.2"}
  # 真实模式 18081
  POST /api/v1/clusters {"name":"qa-badip","tiupHost":"this is not an ip","dbHost":"also,bad","pdEndpoint":"::::"}
  ```
- **期望**：400 + 40001，message 指明"TiUP 主机地址格式不正确"
- **实际**：`HTTP 202 {"code":0,"message":"accepted","data":{"clusterId":4,"jobId":4}}`，任务照常入队，随后必然 3 次采集失败把集群推到 `error`，污染总览「异常集群数」
- **证据**：
  ```
  非法 IP → HTTP=202 body={"code":0,"message":"accepted","data":{"clusterId":4,...}}
  非法 PD 地址 → HTTP=202 body={"code":0,"message":"accepted","data":{"clusterId":5,...}}
  真实模式：非法 IP → HTTP=202 {"clusterId":1}；非法 PD 端点 → 同样 202
  ```
- **涉及文件**：`backend/internal/service/cluster_service.go:410`（`Takeover` 只校验非空/长度/端口范围）、`frontend/src/views/ClusterListView.vue:319`（`tiupHost`/`dbHost` 只有 `required`，无格式规则）
- **修复建议**：后端加 `net.ParseIP` / `url.Parse` 校验（支持域名则用 `net.LookupHost` 异步校验或仅做正则），前端同步补 `pattern`。

#### BUG-03（P1）接管提交不校验连接测试结果，未测试或测试失败均可直接提交

- **复现**：回显「接管集群」弹窗 → 不点「测试连接」→ 直接点「提交」
- **期望**：PRD F-17「**任一失败不允许提交**，可仅保存草稿」——未测试或测试未全通过时，「提交」应禁用，仅允许「保存草稿」
- **实际**：`submitForm()` 只做表单必填校验后即 `clusterApi.create({...form})`，`probeResults` 完全不参与门禁
- **证据**：`frontend/src/views/ClusterListView.vue:451-479`（`submitForm` 中无 `probeResults`/`tested` 判断，无 `saveAsDraft` 之外的分支）
- **涉及文件**：`frontend/src/views/ClusterListView.vue`
- **修复建议**：`const canSubmit = computed(() => probeResults.value.length > 0 && probeResults.value.every(r => r.ok))`，非草稿提交时 `:disabled="!canSubmit"`，并提示"请先完成连接测试且三项全部通过"。

#### BUG-04（P1）集群列表筛选条件未写入 URL query，无法分享与回退

- **复现**：在集群列表设置「状态=healthy」「架构=x86_64」→ 刷新页面 → 筛选全部丢失；复制 URL 发给同事无法复现视图
- **期望**：PRD F-15「条件写入 URL query 便于分享与回退」
- **实际**：`filters` 是本地 `ref` 对象，`ClusterListView.vue` 全文无 `useRoute`/`route.query`/`router.replace`，筛选与 URL 完全脱钩
- **证据**：`grep -rn "useRoute|route.query|router.replace" frontend/src/views/ClusterListView.vue` → **0 命中**
- **涉及文件**：`frontend/src/views/ClusterListView.vue`（`filters`、`load`、`search`、`resetFilters`）
- **修复建议**：把 `page/keyword/status/deployMode/arch/sortBy/sortOrder` 同步到 `route.query`，`onMounted` 从 query 初始化 `filters`。

### P2（体验 / 边界）

#### BUG-05（P2）集群健康状态只反映采集通道，实例 Down 时仍显示"正常"，且 `alertCount=0`
- 见 §5 D-01。`mock-test-c` 有 1 个 TiDB 实例 down，`status=healthy/alertCount=0`，但同集群巡检 `conclusion=fail`、`HA-04 实例运行状态` critical。
- 涉及文件：`backend/internal/collector/collector.go:357`（`Derive`，语义正确）/ `backend/internal/service/cluster_service.go`（列表视图未带实例异常计数）/ `frontend/src/views/ClusterListView.vue`

#### BUG-06（P2）集群内子页无独立路由，深链直接落到「页面不存在」
- 复现：浏览器直接访问 `/clusters/1/topology`
- 期望：进入集群详情并激活「拓扑」Tab（PRD 128 行 `/clusters/:id/topology`、PRD 63 行"集群内 9 项二级导航"）
- 实际：后端返回 200 + `index.html`（fallback 正常），但 vue-router 无 `clusters/:id/topology` 路由，落到 `/:pathMatch(.*)*` → 渲染 `NotFoundView`；且 `activeTab` 是本地 `ref('overview')`，Tab 切换不写 URL，刷新永远回到「概览」
- 证据：`frontend/src/router/index.ts:28-33` 只有 `path: 'clusters/:id'`；`ClusterDetailView.vue:101` `const activeTab = ref('overview')`，全文无 `route.query`/`router.replace` 同步
- 涉及文件：`frontend/src/router/index.ts`、`frontend/src/views/ClusterDetailView.vue`

#### BUG-07（P2）不存在的静态资源返回 200 + index.html
- 复现：`curl -o /dev/null -w '%{http_code}' /assets/not-exist-xxx.js` → `200`
- 期望：404（构建产物带哈希，缺失即部署异常，应快速暴露，而非返回 HTML 导致浏览器报 `Unexpected token '<'`）
- 实际：`mountSPA` 对 `/assets/*` 未命中时也回退 `index.html`
- 涉及文件：`backend/internal/api/router.go:195-220`（`mountSPA`）
- 修复建议：`strings.HasPrefix(reqPath, "/assets/")` 且文件不存在时直接 `apiNotFound`/404。

#### BUG-08（P2）列表页缺页内错误态与重试；部分 `load()` 无 `catch`
- 复现：断开后端或让接口 500，观察列表页
- 期望：页内错误占位 + 「重试」按钮（至少保持骨架，不留白屏）
- 实际：仅全局 toast，页面变空白；`ClusterListView.load()`、`ClusterDetailView.load()`、`ClusterVariablesTab.load()` 只有 `try/finally`，无 `catch` → 控制台 unhandled promise rejection
- 涉及文件：`frontend/src/views/ClusterListView.vue:347`、`ClusterDetailView.vue:121`、`cluster/ClusterVariablesTab.vue`

#### BUG-09（P2）集群名无字符集白名单
- 复现：`POST /api/v1/clusters {"name":"x'; DROP TABLE clusters;--", ...}` → 202 受理并入库
- 期望：仅允许字母/数字/`-`/`_`/`.`（PRD 安全设计要求"标识符白名单校验"）
- 实际：只校验非空与 ≤64 字符；GORM 参数化使注入未生效（`clusters` 表完好，已复验），但会污染列表、影响日志/文件名生成（报告文件名含集群名）
- 涉及文件：`backend/internal/service/cluster_service.go:411`

#### BUG-10（P2）强制改密弹窗可通过右上角 X 关闭
- 复现：首登 admin/admin123 → 弹出「首次登录必须修改初始密码」→ 点右上角 X
- 期望：force 模式下不可关闭（只有改密成功才消失）
- 实际：`el-dialog` 未设 `:show-close="false"`，X 可关闭，关闭后可使用平台（与 BUG-01 叠加）
- 涉及文件：`frontend/src/components/ChangePasswordDialog.vue:2-8`

#### BUG-11（P2）集群列表列与 PRD F-14 不完全一致
- 期望：F-14 要求列含"告警信息（有告警时显示数量徽标可展开）"与"创建时间"
- 实际：列有 集群/健康状态/采集状态/TiDB 版本/部署模式/架构/规模/最近采集/操作；**无告警数徽标列**（接口已返回 `alertCount`，前端未渲染）、**无创建时间列**
- 涉及文件：`frontend/src/views/ClusterListView.vue:47-108`

---

## 八、遗留风险（非缺陷，需产品/运维确认）

| # | 风险 | 影响 | 建议 |
| --- | --- | --- | --- |
| R-01 | **默认弱口令 admin/admin123**（`ARCHITECTURE.md:1295` A1 折中） | 未及时改密则任何人可登录 | 需产品对 A1 拍板：接受折中（则必须修 BUG-01/BUG-10）或改 `/setup` 引导页 |
| R-02 | JWT 存于 `localStorage` | XSS 可窃取（当前 `console.log` 已清零，风险面较小） | 后续可改 httpOnly Cookie + CSRF token |
| R-03 | SQLite 单写者：仅支持**单进程**访问同一 db | 多实例共享 db 文件会锁冲突（`writeMu` 跨进程无效） | 部署文档明确"一个 db 文件只允许一个实例"；或采纳 D-02 的 `_txlock=immediate` |
| R-04 | 进程内任务队列（`chan` 200，4 worker），重启丢失 running 任务 | 已由 `job.Recover` 置 interrupted 并重置 collecting（单测覆盖） | 多实例化时必须换外部队列 |
| R-05 | 演示模式会掩盖连通性失败（`test-connection` 恒成功） | 演示环境无法验证网络问题 | 演示环境 UI 明确标注"演示数据，连接测试为模拟结果" |
| R-06 | F-43（参数修改）、F-53（定时巡检）为占位（接口返回 40901 + "P2 能力，MVP 未启用"） | 与 PRD 迭代二规划一致 | 交付说明中显式声明 MVP 范围 |
| R-07 | `npm run build` 不含类型检查（`vue-tsc --noEmit` 为独立脚本，CI 未串联） | 当前实测**类型检查 0 错误、通过**，暂无实际影响；但后续改动可能漏过类型错误 | 在 CI/Makefile 的 `build` 前串联 `npm run typecheck`（已验证可直接跑通） |
| R-08 | 依赖 `element-plus`、`echarts` 单 chunk 超 1 MB | 首屏偏慢 | 已用 `vendor/charts/element` 手动分包，可再按需引入 |

---

## 九、第一轮结论（存档，2026-11-20）

> ⚠️ 本节为**第一轮**结论，原文保留不修改。工程师已按 P1/P2 清单返工，**第二轮回归的真实结论见 §十**。

1. **可交付性：可交付 MVP。** 构建链路（`CGO_ENABLED=0` 单二进制 + 内嵌前端）、核心链路（登录 → RBAC → 接管 → 采集 → 巡检 → 报告下载 → 审计）、三级健康状态机、任务状态机与重启恢复，全部经真实运行验证通过；无 P0。
2. **交付前建议修复**：BUG-01（强制改密后端门禁）、BUG-02（IP 格式校验）、BUG-03（连接测试门禁）——三者均属"PRD 验收要点未满足"，改动量合计约 1 人日以内。
3. **可在下一迭代处理**：BUG-04～BUG-11，以及 R-01 的产品决策。
4. **质量评价**：代码分层清晰（`api / service / repo / collector / job / inspect` 边界明确），`Derive`、`job.Recover`、`repo.Tx` 三个关键点设计有据且经得起压测；`TX`/`Recover` 的注释与实现基本一致（仅"指数退避"措辞需更正）；前后端在安全默认值（幂等迁移、参数化 SQL、凭据 AES 加密、日志脱敏）上做得扎实。
5. **本轮测试自身遗留**：Go 侧覆盖 auth/collector/inspect/job/service/repo 六个包；`internal/api`（handler 层）、`internal/crypto`（AES-GCM）、`internal/pkg/sqlsafe`（标识符白名单）尚无单测，建议下一轮补齐——这三块是纯函数，测试成本低、收益高。

---

## 十、第二轮回归验收（2026-11-21）

> 触发原因：工程师按第一轮 P1/P2 缺陷清单返工，并**额外修改了 QA 的一个测试文件**（`TestJWT_Parse_TamperedSignatureRejected`）。
> 本轮原则不变：**不采信工程师自述，全部独立重跑**。被测代码改动量：后端 9 个文件（含新增 3 个）、前端 8 个文件（含新增 1 个）、`Makefile`、`scripts/smoke.sh`。
> 环境：全新库 `TMP_MOCK=true TMP_DB=/tmp/qa2-tmp.db TMP_ADDR=:18090 TMP_DATA_DIR=/tmp/qa2-data ./bin/tmp`。

### 10.1 本轮测试规模

| 指标 | 第一轮 | 第二轮 | 变化 |
| --- | --- | --- | --- |
| `_test.go` 文件 | 7 | **10** | +3（`guard_test.go`、`validate_test.go`、`probe_token_test.go`） |
| Go 测试函数 | 57 | **75** | +18 |
| 用例数（含子用例） | 78 | **96** | **+18，未减少** |
| 通过率（`-count=1` / `-count=3` / `-race` 三遍均为） | 78/78 | **96 / 96 = 100%** | FAIL=0、SKIP=0 |
| 接口冒烟断言 | 46 | **78** | 逐条 curl，见 10.4 |

```bash
$ go test ./... -count=3            # 6 个包全部 ok，0 失败
$ CGO_ENABLED=1 go test ./... -race -count=1
ok  internal/auth 6.254s / collector 1.347s / inspect 1.335s / job 1.944s / repo 1.645s / service 1.785s
# 顶层 --- PASS 75、子用例 --- PASS 21、--- FAIL 0、--- SKIP 0
```

### 10.2 【重点复核】工程师修改 QA 测试文件是否削弱覆盖

**改动内容**（`backend/internal/auth/auth_test.go:112-127`）：篡改签名的位置由「**末字符**」改为「**首字符**」，并附注释说明末字符存在填充位问题。

**① 工程师的解释是否成立 —— 成立，且我用 20 万样本独立验证。**

JWT 签名是 HMAC-SHA256 的 **32 字节**，`base64.RawURLEncoding` 编码为 **43 个字符**（42×6 = 252 bit，剩下 **4 bit** → 第 43 个字符只承载 4 bit 有效数据 + **2 bit 填充位**）。因此末字符只能是 `A/E/I/M/Q/U/Y/c/g/k/o/s/w/0/4/8` 这 16 个值之一，**每个值概率 1/16**；`A`(000000) 与 `B`(000001) 的**高 4 位相同**，所以「末字符 A→B」解码出的签名**与原始完全一致**，篡改后的 token 仍能通过校验 → 断言必然失败。

我写了一个独立探针（`/tmp/qa-b64probe2`，已删除）跑 200000 个随机签名，实测：

```
样本数: 200000，签名长度: 32 字节，base64url raw 长度: 43
末字符分布: A=12417(6.21%) Q=12449(6.22%) g=12516(6.26%) w=12536(6.27%)
原逻辑（改末字符）解码后签名不变: 12417/200000 = 6.2085%  (1/16.11)
新逻辑（改首字符）解码后签名不变: 0/200000 = 0.00%
```

→ 原测试有 **6.21% ≈ 1/16 的偶发假阴性**（flaky），与工程师自述的「约 1/16」**完全一致**（不是随口估的）。

**② 改后断言是否严格不弱于原断言 —— 是，实际上更严格。**

| 维度 | 原断言（末字符） | 新断言（首字符） |
| --- | --- | --- |
| 被篡改的字符 | 第 43 位（4 bit 有效 + 2 bit 填充） | 第 1 位（6 bit 全有效） |
| 解码后签名是否必然改变 | **否**，1/16 概率不变 | **是**，0.00% 概率不变（20 万样本实测） |
| 断言语义 | `Parse(tampered)` 必须返回 `ErrInvalidToken` | 同左，**未放宽** |
| 稳定性 | flaky（1/16 失败） | 确定性 |

首字符的 6 个 bit 全部参与第一个签名字节，`X→'A'` / `'A'→'B'` 在任何取值下都会改变首字节，因此篡改**必然**使签名不匹配。**结论：接受该改动，无需另行加强。** 原断言属于我第一轮的测试缺陷（`Send To: QA self`），工程师修正方向正确。

**③ 稳定性实测**：

```bash
$ go test ./internal/auth/ -run TestJWT_Parse_TamperedSignatureRejected -count=100
ok  internal/auth  0.335s          # 100 次全绿
$ go test ./internal/auth/ -run TestJWT_Parse -count=50
ok  internal/auth  60.176s         # 全部 JWT 用例 × 50 全绿
```

### 10.3 十二项验收点逐条结果

| # | 验收点 | 结果 | 关键证据 |
| --- | --- | --- | --- |
| 1 | 构建链：`CGO_ENABLED=0 go build ./...`、`go vet ./...`、`make build` 先跑 typecheck | ✅ | `go build` / `go vet` 均无输出；`make build` 步骤顺序实测为 `>> 前端类型检查（vue-tsc --noEmit）` → `>> 构建前端` → `>> 编译后端` → 产物 `bin/tmp`（17 MB）；`go.mod` 仅 `mattn/go-isatty`（终端检测，indirect），**无 `mattn/go-sqlite3`**，`nm bin/tmp \| grep -c mattn` = 0 |
| 2 | 测试 `-count=3` 与 `-race`，用例不少于 78 | ✅ | **96 用例**（75 函数 + 21 子用例），`-count=3` 全绿、`-race` 全绿、0 失败 0 跳过 |
| 3 | BUG-01 强制改密后端拦截 | ✅ | 改密前 `GET /clusters` = **403 / 40302「请先修改初始密码」**；`/auth/me`、`/auth/password`、`/auth/logout` 均 200；改密后新 token `GET /clusters` = 200，`mustChangePassword=false` |
| 4 | BUG-02 非法入参逐项 | ✅ | 16 组用例全部按预期（详见 10.4-A） |
| 5 | BUG-03 testToken 五场景 | ✅ | 缺失→40001；正确→202；改 dbHost/SSH 端口/PD→40001「连接参数已变更」；伪造→40001；草稿无 token→202；admin force→202 且审计 `"force":true`；**operator force→40001（拒绝生效）** |
| 6 | BUG-05 `abnormalTotal` 口径 | ✅ | `/overview/summary` → `abnormalTotal=1, instanceAbnormalTotal=1`，精确命中 `mock-test-c`（status=healthy 但 1 个 TiDB 实例 down）；`clusterTotal=5` **不含 3 个草稿**；卡片文案已写明「状态异常或存在非 Up 实例」 |
| 7 | BUG-06 10 条子页深链 | ✅ | `overview/topology/instances/users/resource-groups/sessions/params/performance/inspection/changes` 全部 `200 text/html` 且含 `<div id="app">`；`/clusters/1` 由路由 `redirect` 到 `cluster-overview`；未知子路径落 `/:pathMatch(.*)*` → `NotFoundView`（**已有兜底页，不会白屏**） |
| 8 | BUG-07 四类路径 | ✅ | `/assets/not-exist.js` → **404** json；`/foo.png` → **404** json；`/api/v1/nope` → **404 / 40401**；`/clusters/1/topology` → **200 text/html** |
| 9 | BUG-09 新建用户强制改密 + admin 自改密不重复置位 | ✅ | 新建 op1 → 登录 `mustChangePassword=true` → `/clusters` **40302** → 改密 → 200；admin `reset-password` 后**重新置位**（再次 40302）；admin 自己 `PUT /auth/password` 后 `mustChangePassword` 仍为 **false**；重置同时吊销旧 token（旧 token → 401） |
| 10 | `scripts/smoke.sh` 是否被改成弱断言 | ⚠️ **本身就是弱断言脚本** | 99 行、`set -euo pipefail`、**`grep -q` 0 处、`[[ ]]` 0 处、`exit 1` 仅 1 处（二进制缺失）**；所有检查都是「打印 HTTP 码/JSON」，没有 `if` 比对。实测能跑通（见 10.5），但**回归时若接口返回 403 它仍会打印并以 0 退出 → 假绿**。列为 P2（N-01） |
| 11 | 权限矩阵回归 | ✅ | viewer 11 个写接口全 40301、读接口全 200；operator 5 项越权（改 admin 资料/改 admin 密码/停用 admin/建用户/自我提权）全 **40301**，`GET /users`、`GET /audit-logs` 也 40301；operator 合法写（建草稿 202、test-connection 200）正常 |
| 12 | 敏感信息复查 | ✅ | `GET /clusters` 响应体 **0 个** `sshPassword/sshKey/dbPassword/token` 字段；`GET /settings/credentials` 仅返回 `hasSshPassword` 等布尔位；审计导出 50 行中匹配 `password` 的 6 行**全是 action 名**（`auth.password.change`/`user.reset_password`），`grep` 明文密码字面量 **0 处** |

**补充：must-change 状态下的完整接口矩阵（回答「`/api/v1/nope` 返回什么」）**

```
GET  /clusters                        403  {"code":40302,...}   ← 业务接口：拦截
GET  /overview/summary                403  {"code":40302,...}
POST /clusters                        403  {"code":40302,...}
POST /users                           403  {"code":40302,...}   ← 注意：40302 先于 RBAC 的 40301
GET  /audit-logs                      403  {"code":40302,...}
GET  /auth/me                         200  ← 白名单
GET  /api/v1/nope                     404  {"code":40401,...}   ← 未注册路径
GET  /api/v1/auth/nope                404  {"code":40401,...}   ← 白名单前缀但未注册
```

**判定：符合预期。** `MustChangePasswordGuard()` 挂在 `authed := v1.Group("", Authenticate(), MustChangePasswordGuard())` 上，属于**路由命中之后**执行；未注册路径直接由 Gin `NoRoute` 处理，返回 40401 而非 40302。理论上这让「未改密用户」可以枚举哪些路由存在（40302 vs 40401 的差异），信息价值极低（路由表本身不敏感），列为 P2 观察项（N-05），**不建议在交付前改**。

### 10.4 关键实测输出

**A. BUG-02 非法入参矩阵（`POST /api/v1/clusters`，16 组）**

```
0-基线（合法）                            HTTP=202  {"code":0,...,"clusterId":4,"draft":true}
1-集群名 1abc（数字开头）                  HTTP=400  40001 集群名格式不合法：需以字母开头，3~40 位…
2-集群名 ab（过短）                       HTTP=400  40001 集群名格式不合法…
3-集群名 41 位                           HTTP=400  40001 集群名格式不合法…
4-集群名含中文                            HTTP=400  40001 集群名格式不合法…
5-tiupHost=999.1.1.1                    HTTP=400  40001 TiUP 主机格式不合法：应为 IPv4/IPv6…
6-tiupHost 空                           HTTP=400  40001 TiUP 主机不能为空
7-tiupPort=65536                        HTTP=400  40001 SSH 端口需在 1~65535 之间
8-tiupPort=-1                           HTTP=400  40001 SSH 端口需在 1~65535 之间
9-tiupPort=0（归一化为 22，通过）           HTTP=202  ← 设计如此：0 表示「用默认值」
10-dbPort=65536                         HTTP=400  40001 数据库端口需在 1~65535 之间
11-pdEndpoint=abc                       HTTP=400  40001 PD 地址格式不合法：应为 host:port…
12-pdEndpoint 无端口                     HTTP=400  40001 PD 地址格式不合法…
13-pdEndpoint=10.40.3.31:abc            HTTP=400  40001 PD 地址的端口需为数字
14-dashboardUrls=["not-a-url"]          HTTP=400  40001 Dashboard 地址必须以 http:// 或 https:// 开头
15-grafanaUrls=["ftp://…"]              HTTP=400  40001 Grafana 地址必须以 http:// 或 https:// 开头
```

> 说明：`tiupPort=0` / `dbPort=0` 被 `validateTakeoverAddresses` 归一化为默认端口（22 / 4000）后**通过**，不是漏校验 —— 这是「0 = 未填 = 默认值」的约定，`-1` 与 `65536` 仍被正确拒绝。所有错误 message 都带**中文字段名**，可直接展示给用户。`not-an-ip` 这类**单标签主机名**按 RFC1123 属合法主机名，会被放行（设计选择，非缺陷）。

**B. BUG-03 testToken 五场景**

```
1-不带 testToken                        HTTP=400  40001 请先完成连接测试：缺少连接测试凭证，请先测试连通性
2-带正确 testToken                      HTTP=202  {"clusterId":6,"draft":false,"jobId":4}
3-换 dbHost（指纹不符）                   HTTP=400  40001 连接参数已变更，请重新测试连通性
4-换 ssh 端口（指纹不符）                  HTTP=400  40001 连接参数已变更，请重新测试连通性
5-换 PD 地址（指纹不符）                   HTTP=400  40001 连接参数已变更，请重新测试连通性
6-伪造签名 + 过期时间戳                   HTTP=400  40001 凭证已过期，请重新测试连通性
7-草稿 saveAsDraft=true 无 testToken    HTTP=202  ← 草稿不采集，跳过
8-admin force=true 无 testToken         HTTP=202  {"forced":true,"clusterId":8}
9-operator force=true 无 testToken      HTTP=400  40001 缺少连接测试凭证   ← 非 admin 的 force 被忽略
```

审计日志印证（`GET /api/v1/audit-logs`，`cluster.takeover` 25 条）：

```
result=success detail={"dbHost":"10.40.3.32","draft":false,"force":true,"tiupHost":"10.40.3.31"}
result=success detail={"dbHost":"10.40.3.32","draft":true,"force":false,"tiupHost":"10.40.3.31"}
result=failed  detail={"error":"40001: 请先完成连接测试：连接参数已变更，请重新测试连通性"}
```

**C. D-02 建议是否落实（`_txlock=immediate`）**

```go
// backend/internal/db/db.go:27
dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"
// backend/internal/repo/tx.go:27  注释已改为「线性退避（attempt*50ms，最多 5 次）」
```

→ 第一轮 D-02 的三条建议**全部落实**：① DSN 加 `_txlock=immediate`；② 保留 `writeMu`（注释仍写明写锁 + 重试）；③ 注释由「指数退避」更正为「线性退避」。

运行时并发写压测（`_txlock=immediate` 生效后，20 并发 × 2 轮 = 40 次 `POST /clusters`）：

```
40 202      # 全部成功
5xx 数: 0
```

**D. BUG-05 口径复核**

```bash
$ GET /api/v1/overview/summary
{"clusterTotal":5,"instanceTotal":57,"hostTotal":10,
 "abnormalTotal":1,"instanceAbnormalTotal":1,"mockMode":true}

$ GET /api/v1/clusters?page=1&pageSize=50
 id=3  mock-test-c  status=healthy  abnInst=1     ← 被 abnormalTotal 正确计入
 id=7  qa-tok-07    status=unknown  abnInst=0  draft=true   ← 草稿不计入 clusterTotal
 ...（共 8 条，其中 3 条草稿）

# 「实例异常 1」徽标直达
$ GET /api/v1/clusters/3/instances?status=abnormal
条数=1 → tidb :4001 down
```

### 10.5 `scripts/smoke.sh` 实跑结果（供交叉验证）

```bash
$ bash scripts/smoke.sh
>> /healthz: {"code":0,...,"status":"ok","version":"1.0.0-mvp"}
>> /readyz : {"code":0,...,"db":"up","status":"ready"}
>> 强制改密守卫（改密前访问业务接口应 40302）
{"code":40302,"data":null,"message":"请先修改初始密码"}          # ✅ 观察正确
>> 总览   {"clusterTotal":3,...,"abnormalTotal":1,...}
>> 集群拓扑 [('pd',3),('tidb',2),('tikv',3),('tiflash',1),('ticdc',1),('prometheus',1),('grafana',1),('alertmanager',1)]
>> 巡检报告 [('RP20261121-0001','mock-prod-a','risk',89.3)]
>> 报告下载 # TiDB 集群巡检报告 …
>> 任务中心 [(4,'inspect','success',100),(3,'collect','success',100),...]
>> /clusters -> 200 text/html     /clusters/1/instances -> 200 text/html
>> /assets/not-exist.js -> 404 application/json
>> 接管缺少连接测试凭证（应 40001） {"code":40001,"message":"请先完成连接测试：…"}
>> 未认证访问（应 40101）          {"code":40101,"data":null,"message":"缺少认证信息"}
>> 冒烟完成
```

**全部现象正确，但没有任何一处做「值比对」** —— 脚本里 `（应 40302）`、`（应 40001）`、`（应 404）` 都只是**注释性文字**，不是断言。工程师本次只是**新增了 3 条打印**（40302 / assets 404 / 缺 testToken 40001），并未加断言，也没把已有断言改弱（原本就没有）。→ **P2（N-01）**。

### 10.6 第二轮新发现的问题

> 第二轮 **未发现 P0 / P1**。以下 6 项均为 P2（体验/工程规范/观察），不影响交付。

| # | 级别 | 问题 | 证据 | 建议 |
| --- | --- | --- | --- | --- |
| N-01 | P2 | `scripts/smoke.sh` 无真断言，失败会「假绿」 | 99 行脚本：`grep -q` 0、`[[ ]]` 0、`exit 1` 仅 1 处；所有 `（应 xxx）` 只是打印 | 每条改成 `code=$(curl -o /dev/null -w '%{http_code}' …)` + `[ "$code" = "403" ] \|\| { echo FAIL; exit 1; }`，约 20 行 |
| N-02 | P2 | **26 个后端业务文件 `gofmt` 未通过**（第一轮已提，本轮未修） | `gofmt -l backend/internal backend/cmd \| grep -v _test.go \| wc -l` → **26**（如 `model/job.go` 的 struct tag 对齐、`api/system_handler.go`）；QA 自己的测试文件已全部 `gofmt -w` | 交付前跑一次 `gofmt -w backend/internal backend/cmd`，纯格式无逻辑风险 |
| N-03 | P2 | **BUG-08 只修了 4/6 个页面**：`ClusterDetailView.loadCluster()`、`cluster/ClusterVariablesTab.load()` 仍只有 `try/finally`、无 `catch`、无页内错误态 | `grep -c 'errorMessage\|catch'` → 两者均 0；已修：`ClusterListView`(6/3)、`HostListView`(5/2)、`ReportListView`(5/2)、`JobListView`(5/3) | 补 `catch (e) { errorMessage.value = … }` + 页内 `el-alert` + 重试按钮 |
| N-04 | P2 | testToken 失败文案不够精确：**伪造签名**与**指纹不符**共用同一句「连接参数已变更」；**operator 传 force** 的提示是「缺少连接测试凭证」而非「仅管理员可跳过」 | `probe_token.go` 签名不匹配 → `errors.New("连接参数已变更，请重新测试连通性")`；实测 operator force → 40001「缺少连接测试凭证」 | 可选优化：签名不匹配单独提示「凭证无效」；operator force 单独提示「跳过连接测试仅管理员可用」 |
| N-05 | P2 | must-change 状态下未注册路径返回 40401（守卫在路由匹配后），可据 40302/40401 差异枚举路由 | 见 10.3 补充矩阵 | 不建议改（改动会把守卫提到 NoRoute 之前，影响面大、收益低） |
| N-06 | P2 | `GET /clusters?status=abnormal` 仍落 `WHERE status='abnormal'` → **恒 0 行**，与 overview 的复合口径（status≠healthy 或存在非 Up 实例）不一致 | `repo/cluster_repo.go:95-96` 直接 `Where("status = ?")`；`GET /overview/summary` 为复合口径 | 当前前端**没有**把「异常集群」卡片做成可点击跳转，故无死链；若后续要做钻取，需先给 `status=abnormal` 加复合分支。**仅作提示，不算缺陷** |

### 10.7 第一轮缺陷闭环情况

| 缺陷 | 级别 | 第二轮状态 | 复验证据 |
| --- | --- | --- | --- |
| BUG-01 强制改密仅前端拦截 | P1 | ✅ **已修复** | `auth/middleware.go` `MustChangePasswordGuard()` + `router.go:63`；实测 403/40302，白名单 3 接口放行；新增 `guard_test.go` 5 个用例覆盖（含白名单条目数锁定为 5，防后续擅自扩容） |
| BUG-02 缺 IP/PD 格式校验 | P1 | ✅ **已修复** | `service/validate.go`（新增 190 行）；16 组用例全通过；新增 `validate_test.go` 6 个函数 |
| BUG-03 不校验连接测试 | P1 | ✅ **已修复** | `service/probe_token.go`（HMAC 无状态凭证，TTL 10 min，指纹 6 字段）；9 组场景全通过；`probe_token_test.go` 7 个函数 |
| BUG-04 筛选未写 URL | P1 | ✅ **已修复** | `utils/query.ts` 的 `syncQuery/queryString/queryInt/queryBool` 已接入 6 个列表页（ClusterList / HostList / ReportList / JobList / Inspection / ClusterInstancesTab），`router.replace` 不产生历史 |
| BUG-05 健康口径 | P2 | ✅ **已修复（按建议方案）** | `abnormalInstanceCount` + `abnormalTotal` + `instanceAbnormalTotal`；列表徽标可点直达实例页 `?status=abnormal`；卡片文案已说明口径 |
| BUG-06 子页无路由 | P2 | ✅ **已修复** | 10 条子路由 + `clusters/:id` → `cluster-overview` redirect + `NotFoundView` 兜底 |
| BUG-07 缺失资源返回 200 | P2 | ✅ **已修复** | `/assets/*`、带扩展名、`/api/` 未命中 → `apiNotFound`（404/40401） |
| BUG-08 列表页错误态 | P2 | ⚠️ **部分修复（4/6）** | 见 N-03 |
| BUG-09 集群名无白名单 | P2 | ✅ **已修复** | `clusterNamePattern = ^[A-Za-z][A-Za-z0-9_-]{2,39}$`；`x'; DROP TABLE clusters;--` 已被拒（40001） |
| BUG-10 强制改密弹窗可 X 关闭 | P2 | ✅ **已修复** | `ChangePasswordDialog.vue:7-9` `:show-close="!force"` + `:close-on-press-escape="!force"` + `handleBeforeClose` 在 force 下 `ElMessage.warning('请先修改初始密码')` 并 `return`（不调用 `done()`） |
| BUG-11 列表列不符 F-14 | P2 | ✅ **已修复** | 已加「创建时间」列（`formatTime(row.createdAt,'YYYY-MM-DD HH:mm')`）；采集状态列内嵌告警徽标（`alertCount>0` 时按 `alertMaxLevel` 着色，MVP 无告警时为 0 不展示） |

**闭环率：10/11 完全修复（91%），1 项部分修复；P0=0、P1=0（第一轮 4 项 P1 全部关闭）。**

### 10.8 第二轮遗留风险（在第一轮 R-01~R-08 基础上增量）

| # | 风险 | 变化 |
| --- | --- | --- |
| R-01 | 默认弱口令 `admin/admin123` | **风险已实质下降**：BUG-01 后端硬拦截 + BUG-10 弹窗不可关，折中方案 A1 的安全性成立。仍需产品对 A1 拍板 |
| R-03 | SQLite 单进程 | **已缓解**：`_txlock=immediate` 已加，跨进程/外部工具访问的锁升级立即失败路径被消除；仍建议部署文档写明「一个 db 文件只允许一个实例」 |
| R-07 | 构建不含类型检查 | **已关闭**：`Makefile` 的 `build: typecheck frontend-build backend-build`，实测 `>> 前端类型检查（vue-tsc --noEmit）` 为第一步 |
| 其余 R-02/04/05/06/08 | — | 无变化，仍按第一轮记录 |
| R-09（新增） | `smoke.sh` 假绿风险 | 见 N-01。若把该脚本接入 CI，会给出虚假的通过信号 —— **接入 CI 前必须补断言** |

### 10.9 第二轮结论

1. **可交付性：✅ 可交付 MVP（条件已满足）。** 第一轮的 4 项 P1 全部关闭，7 项 P2 中 6 项完全修复、1 项部分修复；第二轮 96 个用例（`-count=3` + `-race` 双跑）100% 通过；构建链、RBAC、强制改密、格式校验、连接测试凭证、健康口径、SPA 深链、缺失资源 404、敏感信息掩码全部经真实服务复验通过。**无 P0、无 P1。**
2. **可放行的判断依据**：本轮所有"PRD 验收要点"级别的缺口（F-07 强制改密、F-17 连接测试门禁、F-15 筛选入 URL、F-14 列表列、F-43/53 之外的子页路由）均已由**后端硬约束**而非前端软提示保障 —— 这是与第一轮最本质的差别（绕过 UI 直接调接口也拦得住）。
3. **交付前建议（均 P2，可并行或延后）**：N-02（`gofmt -w`，10 分钟）→ N-03（2 处 `catch` + 错误态，30 分钟）→ N-01（`smoke.sh` 补断言，如要进 CI 则必须做）→ N-04/N-05/N-06（可选优化）。
4. **质量评价（第二轮）**：返工质量高。四项 P1 都不是"打补丁式糊住"，而是按建议做成了**独立可测的中间件 / 校验层 / 无状态凭证**（`MustChangePasswordGuard`、`validate.go`、`probe_token.go`），并主动补了 18 个单元测试（含防"白名单擅自扩容"的锁定断言），可维护性明显优于直接堆 `if`。
5. **对 QA 测试文件的改动评价**：**接受**。工程师没有削弱断言，而是修正了我引入的一处 flaky（1/16 假阴性），并用注释把原因写清（含"为什么不能改末字符"），这属于**合理的交叉复核**，值得保留。

---

## 十一、最终结论（两轮合并）

| 项 | 结论 |
| --- | --- |
| 缺陷总数 | 第一轮 11 条（P0 0 / P1 4 / P2 7）+ 第二轮新增 6 条 P2 |
| 已关闭 | **P1 4/4（100%）、P2 6/7（86%）** |
| 未关闭 | BUG-08 部分（N-03）、N-01、N-02、N-04、N-05、N-06 —— **全部 P2** |
| 阻塞项 | **无** |
| 交付建议 | **可交付。** 建议把 N-02（`gofmt`）与 N-03（2 处 `catch`）作为交付前 30 分钟顺手修；N-01 若要把 `smoke.sh` 接入 CI 则必须先补断言 |

---

### 附：两轮新增文件清单（未改动任何业务代码）

```
# 第一轮（7 个测试文件）
backend/internal/auth/auth_test.go
backend/internal/collector/derive_test.go
backend/internal/collector/mapping_test.go
backend/internal/inspect/checkers_test.go
backend/internal/job/recover_test.go
backend/internal/repo/tx_test.go
backend/internal/service/job_state_test.go

# 第二轮（新增 3 个测试文件，覆盖本轮返工的三个新模块）
backend/internal/auth/guard_test.go          # MustChangePasswordGuard / 白名单
backend/internal/service/validate_test.go    # ValidateClusterName / HostField / Port / PDEndpoint / URL
backend/internal/service/probe_token_test.go # ProbeTokenSigner 签发 / 校验 / TTL / 时钟偏差 / 篡改

docs/TEST-REPORT.md
```

### 附：测试环境清理

**第一轮**
- 两个测试实例（:18080 mock、:18081 real）均已终止
- `/tmp/qa-tmp.db*`、`/tmp/qa-tmp-real.db*`、`/tmp/qa-data*` 已删除
- 临时探针测试 `zz_probe_txlock_test.go` 已删除

**第二轮**
- `:18090` 测试实例已终止（`TMP_DB=/tmp/qa2-tmp.db`）
- `/tmp/qa2-tmp.db*`、`/tmp/qa2-data`、`/tmp/qa-b64probe*` 及所有 `/tmp/qa2-*.tok|json|log|txt` 已删除
- `scripts/smoke.sh` 自启的 `:18080` 实例已随脚本 `trap EXIT` 自动关停（已确认端口无监听）
- 唯一保留的改动：`backend/internal/inspect/checkers_test.go` 经 `gofmt -w` 格式化（注释对齐，无逻辑变化）
