# TMP（TiDB Manage Platform）系统架构设计

| 项目 | 内容 |
| --- | --- |
| 文档版本 | v1.0 |
| 日期 | 2026-11-20 |
| 作者 | 高见远（架构师） |
| 上游输入 | `docs/PRD.md` v1.0（F-01 ~ F-71，P0 36 条） |
| 下游消费者 | 工程师（据此编码）、QA（据此验收） |
| 交付形态 | 单二进制 + 单 SQLite 文件，零外部中间件 |

---

## 1. 架构总览

### 1.1 一句话架构

**一个 Go 二进制内含「Gin REST API + 嵌入式 Vue SPA + 可插拔三通道采集器 + 进程内异步任务引擎」，元数据落单文件 SQLite，凭据 AES-256-GCM 加密，通过 collector 接口在真实集群与 mock 数据源之间无缝切换。**

### 1.2 已定决策（不可推翻，实现时必须遵守）

| # | 决策 | 落地含义 |
| --- | --- | --- |
| D-1 | **F-43 参数修改不进 MVP** | MVP 只提供 F-42 只读查看；`PUT variables` 留接口位但标记 P1，且必须走白名单（仅允许 `max_connections` / `oom-action` / `tidb_mem_quota_query` / `tidb_distsql_scan_concurrency` / `log_slow_query_threshold` 等有限集合），未白名单直接 40301 |
| D-2 | **不做集群级数据权限** | 权限模型 = 角色（admin / operator / viewer）× 操作。所有登录用户可见全部集群。审计表保留 `resource_id` 供未来升级 |
| D-3 | **巡检项用内置可扩展规则** | 6 大类 20+ 项，规则以 YAML 内嵌（`internal/inspect/templates/*.yaml`）+ 代码内建判定函数注册表实现，不做可视化规则编辑器 |
| D-4 | **定时巡检用进程内 ticker** | 复用采集调度器的 ticker 循环，不引入 cron 库、不引入外部调度；F-53 归 P2 |
| D-5 | **数据保留** | 任务（jobs/job_steps/job_logs）30 天、审计 `audit_logs` 180 天、巡检报告永久。进程内每日清理 goroutine 执行，保留期可从 settings 覆盖 |
| D-6 | **版本兼容靠能力探测** | 支持 TiDB 6.5 / 7.1 / 7.5 / 8.5。启动采集时 `Capabilities()` 探测（`SELECT VERSION()` + 试探 `information_schema.resource_groups`），结果驱动 UI 显隐；低版本访问不支持能力返回 **409 + 40901** 且 message 明确（如「当前 TiDB v6.5.4 不支持 Resource Group，需 v7.5.0+」） |
| D-7 | **mock 优先于真实采集落地** | mock collector 必须先于真实采集可用，保证无集群环境也能完整验收 P0 流程 |

### 1.3 已收口的 PRD 待确认项

| PRD 编号 | 结论 |
| --- | --- |
| Q1 凭据维护 | 接管时 DBA 自填，落 `cluster_credentials`（1 集群 1 份）；F-66「凭据管理」页按集群列出并支持更新/测试；仅 admin 可改 |
| Q2 草稿 | 允许仅保存草稿（`clusters.draft=1`），草稿不计入「纳管集群数」与「异常集群数」 |
| Q3 参数修改 | 见 D-1 |
| Q4 巡检项 | 见 D-3，内置 6 类 24 项起步 |
| Q5 告警 | MVP 仅跳转 Grafana，不接 Alertmanager API；F-32 归 P2 |
| Q6 定时巡检 | P2，进程内 ticker |
| Q7 数据权限 | 见 D-2 |
| Q8 保留期 | 见 D-5 |

### 1.4 分层架构图

```mermaid
graph TB
  UI["浏览器 · Vue3 SPA<br/>Element Plus + Pinia + ECharts"]

  subgraph BIN["单二进制 tmp（CGO_ENABLED=0 静态编译，无外部依赖）"]
    direction TB
    subgraph LA["接入层 :8080"]
      SPA["SPA 静态服务<br/>//go:embed web/dist<br/>history fallback → index.html"]
      API["REST API /api/v1/*<br/>Gin Router 分组"]
      MW["中间件链<br/>Recovery → RequestID → Logger → CORS<br/>→ JWT Auth → RBAC → Audit"]
    end
    subgraph LB["应用层"]
      H["Handler 层<br/>参数绑定/校验/统一响应包装"]
      SVC["领域服务层<br/>ClusterService / TiDBObjectService / HostService<br/>InspectService / JobService / UserService / SettingService"]
    end
    subgraph LC["采集适配层"]
      CI["Collector 接口<br/>Probe / Capabilities / Collect"]
      CS["SSHCollector<br/>TiUP meta.yaml + 主机资源"]
      CM["MySQLCollector<br/>go-sql-driver/mysql"]
      CP["PDCollector<br/>PD HTTP API"]
      CK["MockCollector<br/>3 套内置假集群"]
      CN["Normalizer<br/>字段映射 + 三级健康判定"]
    end
    subgraph LD["支撑层"]
      JE["Job Engine<br/>worker pool + 状态机 + 恢复"]
      IE["Inspect Engine<br/>规则注册表 + 报告渲染"]
      SC["Scheduler<br/>采集 ticker / 定时巡检 ticker / 清理 ticker"]
      SEC["auth + crypto<br/>JWT-HS256 · bcrypt · AES-256-GCM"]
    end
    subgraph LE["存储层"]
      RP["Repo（GORM）<br/>AutoMigrate + 显式索引"]
      DB[("SQLite tmp.db<br/>WAL 模式 · 单文件")]
    end
  end

  MON["被纳管环境<br/>TiUP 主机 / TiDB / PD"]

  UI -->|HTTPS /api/v1| API
  UI -->|静态资源| SPA
  API --> MW --> H --> SVC
  SVC --> JE
  SVC --> IE
  SVC --> CI
  SC --> JE
  CI --> CS & CM & CP & CK
  CS & CM & CP --> MON
  CS & CM & CP --> CN
  CK --> CN
  CN --> SVC
  SVC --> RP --> DB
```

**单二进制边界**：`LA / LB / LC / LD / LE` 全部编译进同一可执行文件；外部只依赖被纳管环境网络可达。运行时产物仅 `tmp.db`（+ WAL/SHM）、`data/secret.key`（未设 `TMP_SECRET` 时自动生成）、日志目录。

---

## 2. 技术选型与理由

### 2.1 后端 Go 依赖（`go.mod`，均可在国内网络 `go mod download` 成功）

| 组件 | 选型 | 版本 | 理由 | 替代方案 |
| --- | --- | --- | --- | --- |
| 语言 | Go | 1.22 | 已定；`//go:embed` 与 `errgroup` 成熟 | — |
| HTTP 框架 | `github.com/gin-gonic/gin` | v1.10.0 | 已定；路由分组/中间件/`StaticFS` 满足 SPA 托管 | echo、chi |
| ORM | `gorm.io/gorm` | v1.25.12 | 已定；`AutoMigrate` 免手写迁移 | sqlx + 手写 DDL |
| SQLite 驱动 | `github.com/glebarez/sqlite` | v1.11.0 | **纯 Go**（底层 `modernc.org/sqlite`），`CGO_ENABLED=0` 可交叉编译 | `glebarez/go-sqlite`（更轻，但需自行包 gorm dialector）；`mattn/go-sqlite3`（需 CGO，**禁用**） |
| JWT | `github.com/golang-jwt/jwt/v5` | v5.2.1 | HS256 + `RegisteredClaims`，API 稳定 | `lestrrat-go/jwx`（过重） |
| 密码哈希 | `golang.org/x/crypto/bcrypt` | v0.31.0 | 已定；Cost=10 | argon2（需额外调参） |
| SSH | `golang.org/x/crypto/ssh` | v0.31.0 | 纯 Go，支持密码与私钥，无外部 ssh 二进制依赖 | `go-cmd/ssh`、调 `ssh` 命令（禁止） |
| MySQL 协议 | `github.com/go-sql-driver/mysql` | v1.8.1 | TiDB 完全兼容；支持 `timeout`/`readTimeout` DSN 参数 | — |
| YAML 解析 | `gopkg.in/yaml.v3` | v3.0.1 | 解析 TiUP `meta.yaml` 与内置巡检模板 | — |
| CORS | `github.com/gin-contrib/cors` | v1.7.2 | 开发态前后端分离必需；生产同源可关 | 手写中间件 |
| 并发编排 | `golang.org/x/sync/errgroup` | v0.10.0 | 三通道并行采集 + `context.WithTimeout` | 裸 `sync.WaitGroup` |
| 结构化日志 | 标准库 `log/slog` | Go 1.22 内置 | 零依赖 JSON 日志，含 `request_id` | zap、zerolog |
| Markdown→HTML | 自研极简渲染 | — | 巡检报告结构固定（标题/表格/列表），手写 <300 行，避免引入 goldmark | `goldmark`（如允许可替换） |

> **构建注意**：`modernc.org/sqlite` 首次编译较慢（1~3 分钟），产物约 35~45MB；属预期，不是异常。构建命令固定 `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/tmp ./cmd/tmp`。

### 2.2 前端依赖（`package.json`）

| 组件 | 选型 | 版本 | 理由 |
| --- | --- | --- | --- |
| 框架 | `vue` | ^3.4.38 | 已定；`<script setup>` + TS |
| 构建 | `vite` + `@vitejs/plugin-vue` | ^5.4.2 / ^5.1.2 | 已定；`build.outDir` 直指 `../backend/web/dist` |
| 类型 | `typescript` + `vue-tsc` | ~5.5.4 / ^2.1.4 | 类型安全 |
| UI | `element-plus` | ^2.8.1 | 已定；表单/表格/抽屉/时间线一次到位。**采用全量引入**（不引 `unplugin-auto-import`），减少构建链路不确定性 |
| 路由 | `vue-router` | ^4.4.3 | 已定；`createWebHistory` + 路由守卫 |
| 状态 | `pinia` | ^2.2.2 | 已定 |
| 图表 | `echarts` | ^5.5.1 | 已定；按需引入 `core + LineChart + PieChart + CanvasRenderer` |
| HTTP | `axios` | ^1.7.4 | 拦截器注入 token / 401 跳登录 / 统一 toast |
| 时间 | `dayjs` | ^1.11.13 | 时间格式化与相对时间 |
| 样式 | `sass` | ^1.77.8 | 主题变量（深色侧栏） |
| 拓扑 | 自研 SVG/Canvas 组件 | — | 100 节点内无需 `@antv/g6`（体积大、API 变动频繁）；用「分组列布局 + CSS transform 拖拽缩放」约 200 行可控 |

### 2.3 架构模式

- 后端：**分层架构**（Handler → Service → Repo），单向依赖；Service 之间不互相 import，公共能力下沉到 `pkg`。
- 采集：**适配器模式 + 组合模式**（`Collector` 聚合 3 个 `Channel`，`MockCollector` 平级替换）。
- 前端：**MVVM**（Vue 组件 + Pinia store），API 层与视图层解耦。

---

## 3. 代码目录结构

```
tmp-platform/
├─ Makefile                        # build / dev / lint / embed 一条命令
├─ README.md
├─ backend/
│  ├─ go.mod / go.sum
│  ├─ cmd/tmp/main.go              # 唯一入口：加载配置→初始化 DB→恢复任务→启动调度→起 HTTP
│  ├─ web/
│  │  ├─ embed.go                  # //go:embed dist  → var DistFS embed.FS
│  │  └─ dist/.gitkeep             # 前端构建产物落地处（npm run build 直接输出到此）
│  └─ internal/
│     ├─ config/config.go          # 环境变量 + 默认值 + 数据目录解析（TMP_ADDR/TMP_DATA_DIR/TMP_SECRET/TMP_MOCK）
│     ├─ config/version.go         # 版本号与构建时间（ldflags 注入）
│     ├─ model/                    # GORM 结构体，一表一文件
│     ├─ db/db.go                  # 打开 SQLite（WAL/foreign_keys/busy_timeout）、连接池
│     ├─ db/migrate.go             # AutoMigrate + 显式索引与唯一约束
│     ├─ db/seed.go                # 内置巡检模板/巡检项、默认 settings 灌入（幂等）
│     ├─ repo/                     # 数据访问，接口 + gorm 实现，一聚合一根文件
│     ├─ service/                  # 领域服务：业务规则、事务边界、审计写入
│     ├─ collector/
│     │  ├─ collector.go           # Collector/Channel 接口、Target、Snapshot、Capabilities
│     │  ├─ composite.go           # 三通道 errgroup 并行聚合 + 降级判定
│     │  ├─ ssh.go                 # SSH 通道：TiUP meta.yaml、主机资源、连通性
│     │  ├─ mysql.go               # MySQL 通道：版本/集群信息/用户/参数/会话/RG
│     │  ├─ pd.go                  # PD 通道：stores/cluster status/members/replication_mode
│     │  ├─ mock.go                # mock 通道：3 套内置假集群
│     │  ├─ mapping.go             # meta.yaml → Snapshot 字段映射与归一化
│     │  └─ cap.go                 # 版本号解析 → Capabilities
│     ├─ job/
│     │  ├─ engine.go              # worker pool、队列、状态机、取消
│     │  ├─ types.go               # JobType 枚举、Step 定义、Handler 注册表
│     │  ├─ handlers.go            # 各类任务的步骤编排（takeover/collect/inspect/ssh_check/...）
│     │  └─ recover.go             # 进程重启后 running→interrupted、pending→重新入队
│     ├─ inspect/
│     │  ├─ engine.go              # 执行器：按模板跑全部项，收集结果、算通过率
│     │  ├─ checkers.go            # 判定函数注册表 map[code]Checker
│     │  ├─ templates/default.yaml # 6 类 24 项内置模板（embed）
│     │  └─ render.go              # Markdown / HTML 报告渲染
│     ├─ auth/
│     │  ├─ jwt.go                 # 签发/解析/token_version 失效
│     │  ├─ password.go            # bcrypt + 密码策略校验
│     │  ├─ rbac.go                # 角色→权限集合、Action 常量
│     │  └─ middleware.go          # JWT 中间件、RequireRole、CurrentUser
│     ├─ crypto/aesgcm.go          # AES-256-GCM 加解密 + 密钥加载/生成 + 掩码
│     ├─ api/
│     │  ├─ router.go              # 路由注册总表（唯一路由真相来源）
│     │  ├─ response.go            # {code,message,data} 包装、ErrorCode 常量、分页助手
│     │  ├─ middleware.go          # RequestID/Logger/Recovery/CORS/Audit
│     │  └─ *_handler.go           # auth/user/overview/cluster/tidbobj/host/inspect/job/setting
│     └─ pkg/
│        ├─ logger/                # slog 封装 + 脱敏 Hook
│        ├─ idgen/                 # 报告号/运行号（IR20261120-0001）生成
│        ├─ retry/                 # 指数退避（采集重试）
│        ├─ sshcmd/                # 固定命令白名单执行器（禁止字符串拼接）
│        └─ sqlsafe/               # 标识符白名单校验（用户名/库名/RG 名）
├─ frontend/
│  ├─ package.json / vite.config.ts / tsconfig.json / index.html
│  ├─ .env.development             # VITE_API_BASE=http://localhost:8080
│  └─ src/
│     ├─ main.ts / App.vue
│     ├─ api/                      # 一模块一文件，函数名 camelCase：auth.ts cluster.ts tidb.ts host.ts inspect.ts job.ts setting.ts
│     ├─ types/                    # 与后端 DTO 对应的 TS interface
│     ├─ router/index.ts           # 路由表 + 守卫（登录态/角色/集群模式）
│     ├─ stores/                   # auth / app / cluster / task / meta
│     ├─ layouts/                  # GlobalLayout.vue（7 项导航）ClusterLayout.vue（9 项集群导航）
│     ├─ views/                    # 一页面一目录，见 §8.1
│     ├─ components/               # StatusDot / EmptyState / JobSteps / TopologyCanvas / PageHeader / MetricCard
│     ├─ styles/                   # variables.scss / element-override.scss / global.scss
│     └─ utils/                    # request.ts（axios）/ format.ts / permission.ts / download.ts
└─ docs/                           # PRD.md / ARCHITECTURE.md
```

---

## 4. 数据模型

### 4.1 DDL（SQLite 方言，GORM AutoMigrate 等价可建）

```sql
-- 1) 平台用户（平台自身账号，非 TiDB 用户）
CREATE TABLE platform_users (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  username             TEXT    NOT NULL,                      -- 统一小写存储
  display_name         TEXT    NOT NULL,
  password_hash        TEXT    NOT NULL,                      -- bcrypt
  role                 TEXT    NOT NULL DEFAULT 'viewer',     -- admin|operator|viewer
  status               TEXT    NOT NULL DEFAULT 'active',     -- active|disabled
  must_change_password INTEGER NOT NULL DEFAULT 0,
  failed_login_count   INTEGER NOT NULL DEFAULT 0,
  locked_until         DATETIME,
  last_login_at        DATETIME,
  last_login_ip        TEXT,
  token_version        INTEGER NOT NULL DEFAULT 0,            -- 改密/禁用 +1 使旧 token 失效
  created_at           DATETIME NOT NULL,
  updated_at           DATETIME NOT NULL
);
CREATE UNIQUE INDEX uk_users_username ON platform_users(username);

-- 2) 集群（聚合根）
CREATE TABLE clusters (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  name              TEXT    NOT NULL,                          -- 展示名，保留大小写
  name_lower        TEXT    NOT NULL,                          -- 小写唯一键；移除纳管时改写为 name||'#removed#'||id
  draft             INTEGER NOT NULL DEFAULT 0,                -- 1=仅保存草稿，不计入统计
  tidb_version      TEXT,
  deploy_mode       TEXT    NOT NULL DEFAULT 'unknown',        -- majority|dr_auto_sync|unknown
  cpu_arch          TEXT,                                      -- x86_64|aarch64
  component_count   INTEGER NOT NULL DEFAULT 0,
  host_count        INTEGER NOT NULL DEFAULT 0,
  instance_count    INTEGER NOT NULL DEFAULT 0,
  tiup_host         TEXT    NOT NULL,
  tiup_port         INTEGER NOT NULL DEFAULT 22,
  tiup_user         TEXT    NOT NULL,
  tiup_dir          TEXT,
  db_host           TEXT,
  db_port           INTEGER NOT NULL DEFAULT 4000,
  db_user           TEXT,
  pd_endpoint       TEXT,                                      -- http://ip:2379
  dashboard_urls    TEXT,                                      -- JSON array
  prometheus_urls   TEXT,                                      -- JSON array（可多个）
  grafana_urls      TEXT,                                      -- JSON array（可多个）
  alertmanager_urls TEXT,                                      -- JSON array
  status            TEXT    NOT NULL DEFAULT 'unknown',        -- healthy|degraded|error|unknown
  collect_status    TEXT    NOT NULL DEFAULT 'unknown',        -- healthy|degraded|error
  collect_fail_streak INTEGER NOT NULL DEFAULT 0,
  collecting        INTEGER NOT NULL DEFAULT 0,                -- 并发刷新幂等标记
  last_collect_at   DATETIME,
  last_success_at   DATETIME,
  last_collect_error TEXT,
  capabilities      TEXT,                                      -- JSON: Capabilities 快照
  fetched_at        DATETIME,                                  -- 数据截至（降级展示用）
  created_by        INTEGER,
  created_at        DATETIME NOT NULL,
  updated_at        DATETIME NOT NULL,
  deleted_at        DATETIME,                                  -- 移除纳管=软删，报告保留
  FOREIGN KEY (created_by) REFERENCES platform_users(id)
);
CREATE UNIQUE INDEX uk_clusters_name_lower ON clusters(name_lower);
CREATE INDEX idx_clusters_status ON clusters(status);
CREATE INDEX idx_clusters_deleted ON clusters(deleted_at);

-- 3) 集群凭据（1:1，密文存储）
CREATE TABLE cluster_credentials (
  id                  INTEGER PRIMARY KEY AUTOINCREMENT,
  cluster_id          INTEGER NOT NULL,
  ssh_password_enc    TEXT,                                    -- AES-256-GCM base64
  ssh_key_enc         TEXT,
  db_password_enc     TEXT,
  key_version         INTEGER NOT NULL DEFAULT 1,
  updated_by          INTEGER,
  created_at          DATETIME NOT NULL,
  updated_at          DATETIME NOT NULL,
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE,
  FOREIGN KEY (updated_by) REFERENCES platform_users(id)
);
CREATE UNIQUE INDEX uk_cred_cluster ON cluster_credentials(cluster_id);

-- 4) 主机（全局，跨集群共享同一 IP）
CREATE TABLE hosts (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  ip                   TEXT    NOT NULL,
  hostname             TEXT,
  os_user              TEXT,
  cpu_arch             TEXT,
  cpu_cores            INTEGER,
  memory_gb            REAL,
  disk_total_gb        REAL,
  cpu_usage_pct        REAL,
  mem_usage_pct        REAL,
  disk_usage_pct       REAL,
  ssh_status           TEXT    NOT NULL DEFAULT 'unknown',      -- ok|fail|unknown
  ssh_checked_at       DATETIME,
  ssh_error            TEXT,
  resource_fetched_at  DATETIME,
  created_at           DATETIME NOT NULL,
  updated_at           DATETIME NOT NULL
);
CREATE UNIQUE INDEX uk_hosts_ip ON hosts(ip);
CREATE INDEX idx_hosts_arch ON hosts(cpu_arch);

-- 5) 实例（集群拓扑的最小单元，采集全量替换）
CREATE TABLE instances (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  cluster_id  INTEGER NOT NULL,
  host_id     INTEGER,
  host_ip     TEXT    NOT NULL,
  component   TEXT    NOT NULL,                                 -- pd|tidb|tikv|tiflash|ticdc|grafana|prometheus|alertmanager
  role        TEXT,                                             -- leader|follower|service（可空）
  port        INTEGER NOT NULL,
  status_port INTEGER,
  status      TEXT    NOT NULL DEFAULT 'unknown',                -- up|down|unhealthy|unknown
  version     TEXT,
  started_at  DATETIME,
  az          TEXT,
  deploy_dir  TEXT,
  data_dir    TEXT,
  raw         TEXT,                                             -- JSON 原始标签
  fetched_at  DATETIME NOT NULL,
  created_at  DATETIME NOT NULL,
  updated_at  DATETIME NOT NULL,
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE,
  FOREIGN KEY (host_id) REFERENCES hosts(id)
);
CREATE UNIQUE INDEX uk_instances ON instances(cluster_id, host_ip, port, component);
CREATE INDEX idx_instances_cluster ON instances(cluster_id);
CREATE INDEX idx_instances_host ON instances(host_id);

-- 6) TiDB 用户快照（来自 mysql.user）
CREATE TABLE tidb_users (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  cluster_id      INTEGER NOT NULL,
  user_name       TEXT    NOT NULL,
  host            TEXT    NOT NULL,
  auth_plugin     TEXT,
  account_locked  TEXT,                                          -- Y|N
  password_expired TEXT,
  resource_group  TEXT,                                          -- <v7.5 为 NULL
  fetched_at      DATETIME NOT NULL,
  created_at      DATETIME NOT NULL,
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uk_tidb_users ON tidb_users(cluster_id, user_name, host);

-- 7) Resource Group 快照（v7.5+）
CREATE TABLE resource_groups (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  cluster_id  INTEGER NOT NULL,
  name        TEXT    NOT NULL,
  ru_per_sec  TEXT,
  priority    TEXT,
  burstable   INTEGER NOT NULL DEFAULT 0,
  user_count  INTEGER NOT NULL DEFAULT 0,
  status      TEXT,
  fetched_at  DATETIME NOT NULL,
  created_at  DATETIME NOT NULL,
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uk_rg ON resource_groups(cluster_id, name);

-- 8) 会话快照（processlist）
CREATE TABLE sessions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  cluster_id  INTEGER NOT NULL,
  session_id  INTEGER NOT NULL,
  user_name   TEXT,
  host        TEXT,
  db          TEXT,
  command     TEXT,
  time_sec    INTEGER NOT NULL DEFAULT 0,
  state       TEXT,
  sql_digest  TEXT,                                              -- 截断到 200 字符
  fetched_at  DATETIME NOT NULL,
  created_at  DATETIME NOT NULL,
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uk_sessions ON sessions(cluster_id, session_id);

-- 9) 全局变量快照（SHOW GLOBAL VARIABLES）
CREATE TABLE variables (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  cluster_id    INTEGER NOT NULL,
  name          TEXT    NOT NULL,
  value         TEXT,
  default_value TEXT,
  scope         TEXT,                                            -- global|session|both
  is_dynamic    INTEGER NOT NULL DEFAULT 1,
  description   TEXT,
  differs       INTEGER NOT NULL DEFAULT 0,                      -- value != default_value
  fetched_at    DATETIME NOT NULL,
  created_at    DATETIME NOT NULL,
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uk_variables ON variables(cluster_id, name);

-- 10) 任务
CREATE TABLE jobs (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  type             TEXT    NOT NULL,   -- takeover|collect|inspect|ssh_check|report|create_tidb_user|drop_tidb_user|kill_session|reset_tidb_password
  cluster_id       INTEGER,
  target_type      TEXT,               -- cluster|host|session|tidb_user|inspection
  target_label     TEXT,               -- 目标对象展示名（集群名/主机IP/用户）
  status           TEXT    NOT NULL DEFAULT 'pending', -- pending|running|success|failed|canceled|interrupted
  progress         INTEGER NOT NULL DEFAULT 0,         -- 0-100 单调递增
  current_step     TEXT,
  error_summary    TEXT,
  payload          TEXT,               -- JSON 入参
  result           TEXT,               -- JSON 产出（如新建的 user id）
  cancel_requested INTEGER NOT NULL DEFAULT 0,
  retry_of         INTEGER,
  created_by       INTEGER,
  created_by_name  TEXT,
  started_at       DATETIME,
  finished_at      DATETIME,
  created_at       DATETIME NOT NULL,
  updated_at       DATETIME NOT NULL,
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE SET NULL,
  FOREIGN KEY (retry_of) REFERENCES jobs(id)
);
CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_cluster ON jobs(cluster_id);
CREATE INDEX idx_jobs_created ON jobs(created_at);
CREATE INDEX idx_jobs_type ON jobs(type);

-- 11) 任务步骤
CREATE TABLE job_steps (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id      INTEGER NOT NULL,
  seq         INTEGER NOT NULL,
  name        TEXT    NOT NULL,
  status      TEXT    NOT NULL DEFAULT 'pending',   -- pending|running|success|failed|skipped
  progress    INTEGER NOT NULL DEFAULT 0,
  error       TEXT,
  started_at  DATETIME,
  finished_at DATETIME,
  created_at  DATETIME NOT NULL,
  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uk_job_steps ON job_steps(job_id, seq);

-- 12) 任务日志（补充表：承载 F-62，避免 job_steps 行膨胀）
CREATE TABLE job_logs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id     INTEGER NOT NULL,
  step_seq   INTEGER,
  level      TEXT    NOT NULL DEFAULT 'info',       -- info|warn|error
  message    TEXT    NOT NULL,
  created_at DATETIME NOT NULL,
  FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX idx_job_logs ON job_logs(job_id, id);

-- 13) 巡检模板
CREATE TABLE inspect_templates (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  code        TEXT    NOT NULL,                     -- default_full
  name        TEXT    NOT NULL,
  description TEXT,
  builtin     INTEGER NOT NULL DEFAULT 1,           -- 内置模板不可删
  enabled     INTEGER NOT NULL DEFAULT 1,
  item_count  INTEGER NOT NULL DEFAULT 0,
  created_at  DATETIME NOT NULL,
  updated_at  DATETIME NOT NULL
);
CREATE UNIQUE INDEX uk_tpl_code ON inspect_templates(code);

-- 14) 巡检项（规则定义）
CREATE TABLE inspect_items (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  template_id INTEGER NOT NULL,
  code        TEXT    NOT NULL,                     -- SEC-01 等
  name        TEXT    NOT NULL,
  category    TEXT    NOT NULL,                     -- system|tidb_config|security|capacity|ha|version
  check_method TEXT   NOT NULL,                     -- sql|ssh|pd_api
  severity    TEXT    NOT NULL,                     -- critical|warning
  expr        TEXT    NOT NULL,                     -- 判定表达式（可读形式，如 "value >= 3"）
  expect      TEXT,                                 -- 期望值描述
  suggestion  TEXT    NOT NULL,                     -- 修复建议（不允许空）
  doc_url     TEXT,
  applies     TEXT,                                 -- 适用条件，如 min_version:7.5.0 / arch:aarch64
  sort_order  INTEGER NOT NULL DEFAULT 0,
  enabled     INTEGER NOT NULL DEFAULT 1,
  created_at  DATETIME NOT NULL,
  updated_at  DATETIME NOT NULL,
  FOREIGN KEY (template_id) REFERENCES inspect_templates(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uk_item_code ON inspect_items(template_id, code);

-- 15) 巡检运行
CREATE TABLE inspect_runs (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  run_no         TEXT    NOT NULL,                  -- IR20261120-0001
  template_id    INTEGER NOT NULL,
  cluster_id     INTEGER,
  cluster_name   TEXT    NOT NULL,                  -- 快照，集群移除后仍可展示
  trigger_type   TEXT    NOT NULL DEFAULT 'manual', -- manual|scheduled
  job_id         INTEGER,
  status         TEXT    NOT NULL DEFAULT 'pending',-- pending|running|success|failed
  total_count    INTEGER NOT NULL DEFAULT 0,
  critical_count INTEGER NOT NULL DEFAULT 0,
  warning_count  INTEGER NOT NULL DEFAULT 0,
  pass_count     INTEGER NOT NULL DEFAULT 0,
  na_count       INTEGER NOT NULL DEFAULT 0,
  unknown_count  INTEGER NOT NULL DEFAULT 0,
  pass_rate      REAL    NOT NULL DEFAULT 0,        -- 一位小数
  conclusion     TEXT,                              -- pass|risk|fail
  started_at     DATETIME,
  finished_at    DATETIME,
  created_at     DATETIME NOT NULL,
  FOREIGN KEY (cluster_id) REFERENCES clusters(id) ON DELETE SET NULL,
  FOREIGN KEY (template_id) REFERENCES inspect_templates(id)
);
CREATE UNIQUE INDEX uk_runs_no ON inspect_runs(run_no);
CREATE INDEX idx_runs_cluster ON inspect_runs(cluster_id);
CREATE INDEX idx_runs_created ON inspect_runs(created_at);

-- 16) 巡检结果明细
CREATE TABLE inspect_results (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id        INTEGER NOT NULL,
  item_code     TEXT    NOT NULL,
  item_name     TEXT    NOT NULL,
  category      TEXT    NOT NULL,
  severity      TEXT    NOT NULL,                   -- 项定义分级: critical|warning
  level         TEXT    NOT NULL,                   -- 判定结果: critical|warning|pass|na|unknown
  actual_value  TEXT,
  expected_value TEXT,
  evidence      TEXT,                               -- 证据（SQL 结果/命令输出片段）
  suggestion    TEXT,
  duration_ms   INTEGER NOT NULL DEFAULT 0,
  created_at    DATETIME NOT NULL,
  FOREIGN KEY (run_id) REFERENCES inspect_runs(id) ON DELETE CASCADE
);
CREATE INDEX idx_results_run ON inspect_results(run_id, level);

-- 17) 巡检报告
CREATE TABLE inspect_reports (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  report_no    TEXT    NOT NULL,                    -- RP20261120-0001
  run_id       INTEGER NOT NULL,
  cluster_id   INTEGER,
  cluster_name TEXT    NOT NULL,
  status       TEXT    NOT NULL DEFAULT 'ready',    -- ready|failed|generating
  content_md   TEXT,
  content_html TEXT,
  file_size    INTEGER NOT NULL DEFAULT 0,
  error        TEXT,
  generated_at DATETIME,
  created_at   DATETIME NOT NULL,
  FOREIGN KEY (run_id) REFERENCES inspect_runs(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX uk_reports_no ON inspect_reports(report_no);
CREATE UNIQUE INDEX uk_reports_run ON inspect_reports(run_id);

-- 18) 审计日志（不可改不可删）
CREATE TABLE audit_logs (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER,
  username      TEXT,
  role          TEXT,
  action        TEXT    NOT NULL,                   -- auth.login / cluster.takeover / tidb_user.create ...
  resource_type TEXT,                               -- cluster|host|tidb_user|session|variable|user|setting|inspection|job
  resource_id   TEXT,
  resource_name TEXT,
  result        TEXT    NOT NULL DEFAULT 'success', -- success|failed
  detail        TEXT,                               -- JSON，密码类字段一律 '***'
  ip            TEXT,
  user_agent    TEXT,
  request_id    TEXT,
  created_at    DATETIME NOT NULL
);
CREATE INDEX idx_audit_created ON audit_logs(created_at);
CREATE INDEX idx_audit_user ON audit_logs(username);
CREATE INDEX idx_audit_action ON audit_logs(action);
CREATE INDEX idx_audit_resource ON audit_logs(resource_type, resource_id);

-- 19) 系统设置（K/V）
CREATE TABLE settings (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  key        TEXT    NOT NULL,
  value      TEXT,
  updated_by INTEGER,
  updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX uk_settings_key ON settings(key);
```

**settings 内置键**：`collect.interval_sec=300`、`collect.timeout_sec=10`、`collect.concurrency=4`、`collect.retry=1`、`auth.jwt_ttl_hours=8`、`auth.idle_timeout_min=30`、`mode.mock=false`、`retention.jobs_days=30`、`retention.audit_days=180`、`threshold.cpu_pct=80`、`threshold.disk_pct=85`、`cluster.error_streak=3`、`setup.completed=false`。

> **表数量：19 张**（你给定的 18 张 + `job_logs`：F-62 要求「>5000 行仍流畅滚动」，日志必须独立成表，否则 `job_steps` 行会膨胀）。

### 4.2 ER 图

```mermaid
erDiagram
  platform_users ||--o{ clusters : "created_by"
  platform_users ||--o{ jobs : "created_by"
  platform_users ||--o{ audit_logs : "产生的审计"
  clusters ||--|| cluster_credentials : "1:1 凭据"
  clusters ||--o{ instances : "拓扑"
  clusters ||--o{ tidb_users : "用户快照"
  clusters ||--o{ resource_groups : "RG 快照"
  clusters ||--o{ sessions : "会话快照"
  clusters ||--o{ variables : "变量快照"
  clusters ||--o{ jobs : "任务目标"
  clusters ||--o{ inspect_runs : "巡检"
  hosts ||--o{ instances : "承载"
  jobs ||--o{ job_steps : "步骤"
  jobs ||--o{ job_logs : "日志"
  inspect_templates ||--o{ inspect_items : "含项"
  inspect_templates ||--o{ inspect_runs : "被引用"
  inspect_runs ||--o{ inspect_results : "明细"
  inspect_runs ||--|| inspect_reports : "1:1 报告"

  platform_users {
    int64 id PK
    string username UK
    string role "admin|operator|viewer"
    int token_version
  }
  clusters {
    int64 id PK
    string name_lower UK
    string status "healthy|degraded|error|unknown"
    string deploy_mode
    datetime fetched_at
    datetime deleted_at "软删"
  }
  instances {
    int64 id PK
    int64 cluster_id FK
    int64 host_id FK
    string component
    int port
    string status
  }
  hosts {
    int64 id PK
    string ip UK
    string ssh_status
  }
  jobs {
    int64 id PK
    string type
    string status
    int progress
    int64 retry_of FK
  }
  job_steps {
    int64 id PK
    int64 job_id FK
    int seq
    string status
  }
  inspect_runs {
    int64 id PK
    string run_no UK
    int64 cluster_id FK
    string conclusion
    float pass_rate
  }
  inspect_reports {
    int64 id PK
    string report_no UK
    int64 run_id FK
    string status
  }
  audit_logs {
    int64 id PK
    string action
    string resource_type
    string result
  }
```

---

## 5. REST API 契约

### 5.1 通用约定

| 项 | 约定 |
| --- | --- |
| 前缀 | 全部业务接口 `/api/v1`；探针 `/healthz`、`/readyz` 不带前缀 |
| 响应包 | `{"code":0,"message":"ok","data":...}`，HTTP 状态码与 code 双轨（见 5.2） |
| 分页 | Query `page`（默认 1）、`pageSize`（默认 20，上限 200）；响应 `data:{list:[],total,page,pageSize}` |
| 认证 | `Authorization: Bearer <jwt>`；白名单仅 login/setup/healthz |
| 时间 | RFC3339（`2026-11-20T22:55:01+08:00`） |
| 追踪 | 请求头 `X-Request-Id`（缺省由服务端生成），回写响应并写入日志/审计 |
| 角色 | A=admin，O=operator，V=viewer，—=无需认证 |

### 5.2 错误码表

| code | HTTP | 含义 | 典型场景 |
| --- | --- | --- | --- |
| 0 | 200 | 成功 | — |
| 40001 | 400 | 参数错误 | 端口越界、密码策略不满足、集群名重复（格式类） |
| 40101 | 401 | 未认证/凭证错误/token 过期或被吊销 | 登录失败、token_version 不匹配 |
| 40301 | 403 | 无权限 | 越权操作、非白名单参数修改 |
| 40401 | 404 | 资源不存在 | 集群 ID / 任务 ID / 报告号不存在 |
| 40901 | 409 | 版本不支持 / 状态冲突 | RG 查询在 v6.5（D-6）、集群名已存在、任务已结束不可取消 |
| 42901 | 429 | 触发限流或锁定 | 连续 5 次登录失败，账号锁 10 分钟（data 带 `lockedUntil`） |
| 50001 | 500 | 内部错误 | 未捕获异常（不暴露堆栈） |
| 50201 | 502 | 采集失败 | SSH/DB/PD 通道失败，data 带 `channel`、`reason` |

### 5.3 认证与初始化（F-01/02/07）

| 方法 | 路径 | 说明 | 角色 |
| --- | --- | --- | --- |
| GET | `/api/v1/setup/status` | 是否已完成初始化（无用户→false） | — |
| POST | `/api/v1/setup/init` | 创建首个 admin，body `{username,displayName,password}`；已初始化返回 40901 | — |
| POST | `/api/v1/auth/login` | body `{username,password}`；成功返回 `{token,expiresAt,user}` | — |
| POST | `/api/v1/auth/logout` | 记录审计并使当前 token 失效（客户端丢弃 + 服务端可选黑名单） | A/O/V |
| GET | `/api/v1/auth/me` | 当前用户 + 权限集合 + `mustChangePassword` | A/O/V |
| PUT | `/api/v1/auth/password` | 改自己密码，body `{oldPassword,newPassword}`；成功后 `token_version+1` | A/O/V |

### 5.4 平台用户与角色（F-03/04/05/06/08）

| 方法 | 路径 | 说明 | 角色 |
| --- | --- | --- | --- |
| GET | `/api/v1/users` | 分页 + `keyword`；不返回 password 字段 | A |
| POST | `/api/v1/users` | `{username,displayName,password,role}` | A |
| PUT | `/api/v1/users/:id` | 仅 `displayName`、`role`；最后一个 admin 不可降级 | A |
| PATCH | `/api/v1/users/:id/status` | `{status:active\|disabled}`；禁用后 token 立即失效 | A（P1） |
| POST | `/api/v1/users/:id/reset-password` | `{password}`；返回空 data | A（P1） |
| GET | `/api/v1/roles/matrix` | 角色 × 操作矩阵（只读），与 PRD 8.4 一致 | A/O/V |

### 5.5 总览（F-09/10/11/12/13）

| 方法 | 路径 | 说明 | 角色 |
| --- | --- | --- | --- |
| GET | `/api/v1/overview/summary` | `{clusterTotal,instanceTotal,hostTotal,abnormalTotal,tidbVersionDist}`；不含草稿 | A/O/V |
| GET | `/api/v1/overview/health-distribution` | 环形图数据 `[{status,count}]` | A/O/V（P1） |
| GET | `/api/v1/overview/recent-jobs` | 最近 10 条，进行中置顶 | A/O/V |
| GET | `/api/v1/overview/latest-inspection` | 最近巡检摘要 | A/O/V（P1） |
| GET | `/api/v1/overview/collect-status` | 全量采集时间、成功/失败集群数、`mockMode` | A/O/V（P1） |

### 5.6 集群（F-14 ~ F-32）

| 方法 | 路径 | 说明 | 角色 |
| --- | --- | --- | --- |
| GET | `/api/v1/clusters` | 分页 + `keyword/version/deployMode/status/arch/sortBy/sortOrder` | A/O/V |
| GET | `/api/v1/clusters/options` | 下拉用精简列表 `[{id,name,status}]` | A/O/V |
| POST | `/api/v1/clusters/test-connection` | body=接管表单；返回 `[{scope,ok,latencyMs,message}]`，整体 15s；`pdEndpoint` 留空时并行自动发现并返回 `discovered` | A/O |
| POST | `/api/v1/clusters/discover-pd` | body=接管表单（name/tiupHost/dbHost 等）；返回 `{endpoints[],primary,source,message}`，source ∈ tiup_meta/cluster_info/mock | A/O |
| POST | `/api/v1/clusters` | 接管：`{name,tiupHost,tiupPort,tiupUser,sshPassword,dbHost,dbPort,dbUser,dbPassword,pdEndpoint,saveAsDraft}`；返回 `{clusterId,jobId}` | A/O |
| GET | `/api/v1/clusters/:id` | 详情（凭据仅掩码） | A/O/V |
| PUT | `/api/v1/clusters/:id` | 编辑纳管信息（P1），改后置 `collect_status=degraded` 提示重采 | A/O |
| DELETE | `/api/v1/clusters/:id` | 移除纳管（软删），body `{confirmName}`；不影响真实集群 | A/O（P1） |
| POST | `/api/v1/clusters/:id/refresh` | 建 collect 任务；已有进行中则复用返回同 `jobId` | A/O |
| POST | `/api/v1/clusters/batch-refresh` | body `{ids:[]}`；返回 `[{clusterId,jobId}]` | A/O |
| GET | `/api/v1/clusters/:id/overview` | 基础信息 + 组件版本汇总 + 实例摘要 + 最近任务 + `fetchedAt` | A/O/V |
| GET | `/api/v1/clusters/:id/topology` | `{groups:[{component,nodes:[{id,hostIp,port,status,role,version}]}]}` | A/O/V |
| GET | `/api/v1/clusters/:id/instances` | 分页 + `component/status` 筛选 | A/O/V |
| GET | `/api/v1/clusters/:id/components` | 按组件聚合实例数与版本，含 `versionConsistent` | A/O/V |
| GET | `/api/v1/clusters/:id/capabilities` | `Capabilities` + `featureVisible` 映射 | A/O/V |
| GET | `/api/v1/clusters/:id/changes` | 变更记录（来自 audit_logs + jobs 聚合） | A/O/V（P1） |
| GET | `/api/v1/clusters/:id/jobs` | 该集群最近任务（默认 5 条） | A/O/V（P1） |
| GET | `/api/v1/clusters/:id/metrics` | `?range=1h\|6h\|24h\|7d` 趋势（P1；MVP 返回 40901「未接入监控」空态） | A/O/V（P1） |
| GET | `/api/v1/clusters/:id/alerts` | 活跃告警（P2，MVP 返回空数组） | A/O/V（P2） |

### 5.7 集群内 TiDB 对象（F-33 ~ F-44）

| 方法 | 路径 | 说明 | 角色 |
| --- | --- | --- | --- |
| GET | `/api/v1/clusters/:id/tidb-users` | 分页 + `keyword`；`resourceGroup` 在 <v7.5 返回 null | A/O/V |
| POST | `/api/v1/clusters/:id/tidb-users` | `{userName,host,password,resourceGroup?,grants?}`；异步任务，返回 `{jobId}` | A/O |
| DELETE | `/api/v1/clusters/:id/tidb-users/:uid` | `:uid` 为本地快照 id；root 与当前连接账号 40301 | A/O |
| PUT | `/api/v1/clusters/:id/tidb-users/:uid/password` | `{password}`；异步任务 | A/O（P1） |
| GET | `/api/v1/clusters/:id/tidb-users/:uid/grants` | `SHOW GRANTS` 实时查询 | A/O/V（P1） |
| POST | `/api/v1/clusters/:id/tidb-users/:uid/grants` | `{scope:'global'\|'db',db?,privs:[]}`；执行后复核差集 | A/O（P1） |
| DELETE | `/api/v1/clusters/:id/tidb-users/:uid/grants` | `{scope,db?,privs:[]}` | A/O（P1） |
| GET | `/api/v1/clusters/:id/resource-groups` | v7.5+ 正常；否则 409+40901 | A/O/V（P1） |
| POST | `/api/v1/clusters/:id/resource-groups/bind` | `{userName,host,resourceGroup}` | A/O（P1） |
| POST | `/api/v1/clusters/:id/resource-groups/unbind` | `{userName,host}` | A/O（P1） |
| GET | `/api/v1/clusters/:id/sessions` | 分页 + `sortBy=time\|id` + `keyword`；>60s Query 标记 | A/O/V（P1） |
| POST | `/api/v1/clusters/:id/sessions/kill` | `{sessionIds:[]}`；异步任务，逐条结果 | A/O（P1） |
| GET | `/api/v1/clusters/:id/variables` | 分页 + `keyword` + `onlyDiff=true` | A/O/V |
| GET | `/api/v1/clusters/:id/variables/export` | 导出 CSV 附件 | A/O/V |
| PUT | `/api/v1/clusters/:id/variables` | **P1 且白名单**（D-1）；非白名单 40301 | A/O（P1） |
| GET | `/api/v1/clusters/:id/slow-queries` | `?range=&limit=` | A/O/V（P2） |

### 5.8 主机（F-45 ~ F-49）

| 方法 | 路径 | 说明 | 角色 |
| --- | --- | --- | --- |
| GET | `/api/v1/hosts` | 分页 + `clusterId/arch/sshStatus/keyword`；返回角色分布、所属集群列表 | A/O/V |
| GET | `/api/v1/hosts/grouped` | 按集群分组视图 `[{clusterId,clusterName,hosts:[]}]` | A/O/V |
| GET | `/api/v1/hosts/:id` | 详情（P2 页面，接口 MVP 先给） | A/O/V |
| GET | `/api/v1/hosts/:id/metrics` | 资源趋势 | A/O/V（P1） |
| POST | `/api/v1/hosts/ssh-check` | `{hostIds:[]}`；返回 `{jobId}` | A/O（P1） |
| GET | `/api/v1/hosts/:id/ssh-checks` | SSH 检测历史（来自 audit/jobs） | A/O/V（P1） |

### 5.9 巡检（F-50 ~ F-58）

| 方法 | 路径 | 说明 | 角色 |
| --- | --- | --- | --- |
| GET | `/api/v1/inspection/templates` | 模板列表含 `itemCount` | A/O/V（P1） |
| GET | `/api/v1/inspection/templates/:id` | 模板 + 分组项明细 | A/O/V（P1） |
| GET | `/api/v1/inspection/items` | `?templateId=&category=` | A/O/V（P1） |
| POST | `/api/v1/inspection/runs` | `{clusterIds:[],templateId}`；多集群各建一个任务，返回 `[{clusterId,jobId,runNo}]` | A/O |
| GET | `/api/v1/inspection/runs/:id` | 运行概要 + 分级统计 | A/O/V |
| GET | `/api/v1/inspection/reports` | 分页 + `clusterId/from/to`；时间倒序 | A/O/V |
| GET | `/api/v1/inspection/reports/:id` | 报告详情（概要 + 明细 + 建议；`content_html` 单独接口下发） | A/O/V |
| GET | `/api/v1/inspection/reports/:id/download` | `?format=md\|html` → `Content-Disposition` 附件，文件名含集群名与时间戳 | A/O/V |
| POST | `/api/v1/inspection/schedules` | 定时巡检配置（P2） | A（P2） |

### 5.10 任务中心（F-59 ~ F-64）

| 方法 | 路径 | 说明 | 角色 |
| --- | --- | --- | --- |
| GET | `/api/v1/jobs` | 分页 + `type/status/clusterId/mine/from/to`，默认最近 7 天 | A/O/V |
| GET | `/api/v1/jobs/running-count` | 顶栏角标（P1，30s 轮询） | A/O/V（P1） |
| GET | `/api/v1/jobs/:id` | 概要 | A/O/V |
| GET | `/api/v1/jobs/:id/steps` | 步骤列表（按 seq） | A/O/V |
| GET | `/api/v1/jobs/:id/logs` | `?afterSeq=` 增量拉取，返回 `[{seq,level,message,at}]` | A/O/V |
| POST | `/api/v1/jobs/:id/retry` | 失败任务重试，新建任务 `retry_of=原ID` | A/O（P1） |
| POST | `/api/v1/jobs/:id/cancel` | 置 `cancel_requested=1`；已结束返回 40901 | A/O（P1） |

### 5.11 设置 / 凭据 / 审计 / 系统（F-65 ~ F-70）

| 方法 | 路径 | 说明 | 角色 |
| --- | --- | --- | --- |
| GET | `/api/v1/settings/collection` | 采集配置 | A/O/V（非 admin 只读） |
| PUT | `/api/v1/settings/collection` | 修改周期/超时/并发/重试，立即生效 | A（P1） |
| GET | `/api/v1/settings/credentials` | 按集群列出凭据（全掩码） | A |
| PUT | `/api/v1/settings/credentials/:clusterId` | 更新 OS/DB 凭据（需重输明文，加密入库） | A |
| POST | `/api/v1/settings/credentials/:clusterId/test` | 用已存凭据测试连通性 | A |
| GET | `/api/v1/settings/demo-mode` | 当前模式 + 数据源标识 | A/O/V |
| PUT | `/api/v1/settings/demo-mode` | `{enabled}` 二次确认（前端），切换采集器实现 | A（P1） |
| GET | `/api/v1/audit-logs` | 分页 + `username/action/from/to` | A（P1） |
| GET | `/api/v1/audit-logs/export` | 导出 CSV | A（P1） |
| GET | `/api/v1/settings/about` | 版本、构建时间、元数据库、兼容矩阵 | A/O/V（P1） |
| GET | `/api/v1/meta/enums` | 全部枚举字典（前端下拉复用） | A/O/V |
| GET | `/healthz` `/readyz` | 存活/就绪（含 DB ping） | — |

**接口共 85 条定义 / 86 个端点**（`/healthz` 与 `/readyz` 合一行），其中 MVP（P0）**约 60 个**。

---

## 6. 采集链路设计

### 6.1 接口定义（`internal/collector/collector.go`）

```go
type Kind string
const (KindSSH Kind="ssh"; KindMySQL Kind="mysql"; KindPD Kind="pd"; KindMock Kind="mock")

type Target struct {
    ClusterID                    int64
    TiUPHost string; TiUPPort   int; TiUPUser, TiUPPassword, TiUPKey string; TiUPDir string
    DBHost   string; DBPort     int; DBUser,   DBPassword string
    PDHost   string; PDPort     int
}

type ProbeScope string
const (ScopeSSH ProbeScope="ssh"; ScopeMySQL ProbeScope="mysql"; ScopePD ProbeScope="pd")

type ProbeResult struct {
    Scope ProbeScope `json:"scope"`; OK bool `json:"ok"`
    LatencyMS int64   `json:"latencyMs"`; Message string `json:"message"`
    Detail map[string]any `json:"detail,omitempty"`
}

type Capabilities struct {
    TiDBVersion            string            `json:"tidbVersion"`
    Major, Minor           int               `json:"-"`       // 用于比较
    SupportsResourceGroup  bool              `json:"supportsResourceGroup"`   // >=7.5
    SupportsSessionKill    bool              `json:"supportsSessionKill"`     // >=6.5 全量支持
    SupportsPlacementRules bool              `json:"supportsPlacementRules"`
    ProbeErrors            map[string]string `json:"probeErrors,omitempty"`
    ProbedAt               time.Time         `json:"probedAt"`
}

type CollectScope struct{ Meta, Hosts, Users, Variables, Sessions, RGs bool } // 全量采集时全 true

// 单通道适配器
type Channel interface {
    Kind() Kind
    Probe(ctx context.Context, t *Target) (ProbeResult, error)
}

type SSHChannel interface {
    Channel
    FetchTiUPMeta(ctx context.Context, t *Target) (*TiUPMeta, error)
    FetchHostResource(ctx context.Context, t *Target) ([]HostResource, error)
}
type MySQLChannel interface {
    Channel
    Capabilities(ctx context.Context, t *Target) (*Capabilities, error)
    FetchClusterInfo(ctx context.Context, t *Target) (*ClusterInfo, error)
    FetchUsers(ctx context.Context, t *Target) ([]TiDBUserRow, error)
    FetchVariables(ctx context.Context, t *Target) ([]VariableRow, error)
    FetchSessions(ctx context.Context, t *Target) ([]SessionRow, error)
    FetchResourceGroups(ctx context.Context, t *Target) ([]RGRow, error) // 低版本返回 ErrUnsupported
    Exec(ctx context.Context, t *Target, st Statement) error            // P1，仅白名单
}
type PDChannel interface {
    Channel
    FetchStores(ctx context.Context, t *Target) ([]Store, error)
    FetchClusterStatus(ctx context.Context, t *Target) (*PDClusterStatus, error)
    FetchMembers(ctx context.Context, t *Target) ([]PDMember, error)
    FetchReplicationMode(ctx context.Context, t *Target) (string, error)
}

// PDDiscoverer 可选能力：自动发现 PD 地址（接管表单无需用户手填）。
// 真实模式：SSH 读 meta.yaml 的 pd_servers → MySQL 查 information_schema.cluster_info；
// 演示模式：从内置数据集取 PD 实例。
type PDDiscovery struct{ Endpoints []string; Source, Message string }
type PDDiscoverer interface {
    DiscoverPD(ctx context.Context, t *Target) (*PDDiscovery, error)
}

// 对外统一门面（Service 只依赖它）
type Collector interface {
    Kind() Kind
    Probe(ctx context.Context, t *Target, scopes []ProbeScope) ([]ProbeResult, error)
    Capabilities(ctx context.Context, t *Target) (*Capabilities, error)
    Collect(ctx context.Context, t *Target, sc CollectScope) (*Snapshot, error)
}

type Snapshot struct {
    Meta      *TiUPMeta
    Hosts     []HostResource
    Instances []InstanceSnapshot
    Users     []TiDBUserRow; Variables []VariableRow; Sessions []SessionRow; RGs []RGRow
    ClusterInfo *ClusterInfo
    ChanErrors map[Kind]string   // 每通道错误 → 降级判定唯一依据
    FetchedAt  time.Time
}
```

实现映射：`CompositeCollector`（Kind 返回当前模式）聚合 `sshChannel`+`mysqlChannel`+`pdChannel`；`MockCollector` 直接返回预置 `Snapshot`，`ChanErrors` 为空。

### 6.2 三通道采集时序

```mermaid
sequenceDiagram
  autonumber
  participant FE as 前端
  participant API as clusterHandler
  participant SVC as ClusterService
  participant JE as JobEngine
  participant CC as CompositeCollector
  participant SSH as sshChannel
  participant MY as mysqlChannel
  participant PD as pdChannel
  participant RP as Repo

  FE->>API: POST /api/v1/clusters/:id/refresh
  API->>SVC: RefreshCluster(id, operator)
  SVC->>RP: 查该集群 status IN(pending,running) 的 collect 任务
  alt 已存在进行中任务
    RP-->>SVC: job(id=42)
    SVC-->>API: {jobId:42, reused:true}
  else 新建任务
    SVC->>JE: Submit(Job{type:collect, clusterID})
    JE->>RP: INSERT jobs + 5 条 job_steps
    JE-->>SVC: jobId
    SVC->>RP: UPDATE clusters SET collecting=1
    SVC-->>API: {jobId}
  end
  API-->>FE: 202 {jobId}

  JE->>JE: worker 领取 → status=running, progress=5
  JE->>CC: Collect(ctx(timeout=15s), target, all)
  par errgroup 三通道并行（互不阻塞）
    CC->>SSH: ssh dial + read ~/.tiup/storage/cluster/clusters/{cluster}/meta.yaml
    SSH-->>CC: TiUPMeta 或 err
    CC->>SSH: 固定命令 uname -m / nproc / free / df（主机资源）
  and
    CC->>MY: SELECT VERSION() → Capabilities
    CC->>MY: information_schema.cluster_info / cluster_hardware
    CC->>MY: mysql.user / SHOW GLOBAL VARIABLES / processlist
    CC->>MY: information_schema.resource_groups（v7.5 以下忽略错误 → unsupported）
    MY-->>CC: 各数据集或 err
  and
    CC->>PD: GET /pd/api/v1/stores
    CC->>PD: GET /pd/api/v1/cluster/status
    CC->>PD: GET /pd/api/v1/replication_mode/status
    PD-->>CC: stores/status/mode 或 err
  end
  CC->>CC: Normalize(mapping.go) → Snapshot（缺失通道填空 + 记 ChanErrors）
  CC-->>JE: Snapshot
  JE->>RP: 事务：全量替换 instances/快照表；upsert hosts；更新 clusters 状态/计数/时间
  JE->>RP: UPDATE jobs status=success/failed, progress=100
  FE->>API: GET /api/v1/jobs/:id （每 2s 轮询）
  API-->>FE: progress / currentStep
```

### 6.3 TiUP meta.yaml → 数据表字段映射

| meta.yaml 路径 | 目标 | 说明 |
| --- | --- | --- |
| `cluster_version` | `clusters.tidb_version` | 兜底取 MySQL `SELECT VERSION()` |
| `global.arch`（常为空） | `clusters.cpu_arch` | 为空时用 SSH `uname -m` 归一为 `x86_64`/`aarch64` |
| `pd_servers[].host/.port` | `instances(component=pd, port=2379)` | 首个标记 `role=leader`（可被 PD members 校准） |
| `tidb_servers[].host/.port/.status_port/.deploy_dir` | `instances(component=tidb, port=4000)` | `status_port=10080` |
| `tikv_servers[].host/.port/.status_port/.data_dir/.deploy_dir` | `instances(component=tikv, port=20160)` | `status_port=20180` |
| `tiflash_servers[]` | `instances(component=tiflash)` | 无该段则跳过 |
| `cdc_servers[]` 或 `ticdc_servers[]` | `instances(component=ticdc, port=8300)` | 兼容两种键名 |
| `grafana_servers[].host` + `global.grafana_port`(默认 3000) | `clusters.grafana_urls` + `instances(component=grafana)` | 多实例 → JSON 数组 |
| `monitoring_servers[].host` + 9090 | `clusters.prometheus_urls` | 可多个 |
| `alertmanager_servers[].host` + 9093 | `clusters.alertmanager_urls` | 可多个 |
| PD 主机 + 2379 | `clusters.dashboard_urls` = `http://<pd>:2379/dashboard` | TiDB Dashboard 由 PD 提供 |
| `/pd/api/v1/replication_mode/status` | `clusters.deploy_mode` | `majority` / `dr-auto-sync` → 归一 `majority`/`dr_auto_sync` |
| `information_schema.cluster_hardware` | `hosts.cpu_cores/memory_gb` | SSH `nproc/free` 为兜底，DB 优先（无需 SSH） |

**降级兜底**：meta.yaml 读取失败时执行 `tiup cluster display <name> -T` 解析文本表（固定列宽，用正则按空白切分），仍失败则该通道记 error。SSH 执行一律走 `pkg/sshcmd` 的命令白名单（`cat`、`uname`、`nproc`、`free`、`df`、`tiup cluster display`），**路径与集群名必须用 `shellescape` 校验白名单字符 `[A-Za-z0-9._/-]`，禁止任何用户输入拼接**。

### 6.4 健康判定规则（三级状态 + 降级）

| 判定结果 | 条件（按序求值） | `clusters.status` / `collect_status` |
| --- | --- | --- |
| 成功 | `ChanErrors` 为空 | `healthy` / `healthy`；`collect_fail_streak=0` |
| 降级 | 有 `ChanErrors` 但 ≥1 通道成功（Meta 或 MySQL 成功即视为有可用数据） | `degraded` / `degraded`；展示上次快照 + `fetched_at` |
| 失败（本次） | `ChanErrors` 覆盖全部关键通道 | `collect_status=error`，`collect_fail_streak+1` |
| 异常（终态） | `collect_fail_streak >= settings.cluster.error_streak`（默认 3） 或 从未成功（`last_success_at IS NULL`） | `error`（列表红色），仍保留最后快照并置顶告警条 |
| 未知 | 从未采集且无失败记录（刚接管 / 草稿） | `unknown` |

补充：`PD` 失败不单独导致 `degraded`（PRD R-05 将其列为建议项），仅在 `overview` 对应字段显示「—」并给出 `probeErrors.pd`。**降级/异常计入总览「异常集群数」，草稿不计入任何统计。**

### 6.5 超时与重试

| 项 | 值 | 来源 |
| --- | --- | --- |
| 单通道超时 | 10s | `settings.collect.timeout_sec` |
| 单集群整体超时 | 15s（SSH 5s + DB 5s + PD 5s 并行，取 max + 汇总开销） | 常量 `collectTotalTimeout` |
| 连接测试单项超时 | 5s，整体 15s | PRD R-06 |
| 采集失败重试 | 1 次，指数退避 2s | `settings.collect.retry`；仅在「网络类错误」重试，认证类错误不重试 |
| 并发采集 | 默认 4（worker pool 大小），超出排队不失败 | `settings.collect.concurrency` |
| 连接池 | MySQL `SetMaxOpenConns(2)`/`SetConnMaxLifetime(5m)`，每集群用一次性连接串，采集完关闭 | 避免连接泄漏 |

---

## 7. 异步任务模型

### 7.1 状态机

```mermaid
stateDiagram-v2
  [*] --> pending: Submit(入队)
  pending --> running: worker 领取(记录 started_at)
  pending --> canceled: 用户取消
  running --> success: 全部步骤 success
  running --> failed: 步骤失败且无剩余重试
  running --> canceled: 检出 cancel_requested=1
  running --> interrupted: 进程重启恢复(启动扫描)
  success --> [*]
  failed --> [*]
  canceled --> [*]
  interrupted --> [*]
  failed --> pending: retry → 新建 job(retry_of=原ID)，原任务不变
  note right of interrupted
    恢复策略：启动时
    running → interrupted
    pending → 重新入队
  end note
```

### 7.2 Job 类型与步骤编排

| type | 触发 | 步骤（seq 顺序） | 超时 |
| --- | --- | --- | --- |
| `takeover` | 接管提交 F-18 | 1 校验参数 → 2 SSH 连通 → 3 DB 连通 → 4 PD 探测 → 5 读取拓扑 → 6 落库 → 7 完成 | 60s |
| `collect` | 手动刷新 F-21 / 调度周期 / 接管后 | 1 能力探测 → 2 SSH 拓扑 → 3 MySQL 对象 → 4 PD 状态 → 5 归一化落库 | 15s |
| `inspect` | F-52 | 1 加载模板 → 2 采集最新快照（复用 collect 逻辑） → 3 逐项判定（每项一个子步骤，进度按项数均分） → 4 汇总统计 → 5 生成报告 | 120s |
| `ssh_check` | F-48 | 1 解析主机 → 2 逐主机探测（每台一子步骤） → 3 回写 `hosts.ssh_status` | 90s |
| `create_tidb_user` | F-34 | 1 校验标识符白名单 → 2 预检重名 → 3 CREATE USER → 4 授权 → 5 刷新用户快照 | 30s |
| `drop_tidb_user` | F-35 | 1 保护账号校验 → 2 DROP USER → 3 刷新快照 | 30s |
| `kill_session` | F-41 | 1 拉取会话 → 2 逐条 KILL（每条一子步骤，逐条成败） | 30s |
| `reset_tidb_password` | F-36 | 1 校验 → 2 ALTER USER → 3 审计 | 30s |
| `report` | F-55 失败重试 | 1 读取 run → 2 渲染 MD/HTML → 3 落库 | 30s |

### 7.3 引擎参数

| 参数 | 值 | 说明 |
| --- | --- | --- |
| worker 数 | `settings.collect.concurrency`（默认 4） | 启动时固定，改配置需重启（提示用户） |
| 队列容量 | 200（有界 channel） | 满则 `Submit` 阻塞 ≤3s 后返回 42901「任务队列已满」 |
| 进度写入 | 每步开始/结束 + 每 20 行日志落库一次 | 避免 SQLite 写放大 |
| 日志写入 | 批量 200 行或 1s 刷盘 | 单条 ≤ 4096 字节，超长截断 |
| 单任务 panic | `recover` → `failed`，`error_summary=internal panic`，堆栈写日志 | 不拖垮 worker |
| 取消语义 | 不强制 kill goroutine，在步骤边界检查 `cancel_requested`，置 `canceled` | 简单可控 |

### 7.4 进程重启恢复（`job/recover.go`）

1. `UPDATE jobs SET status='interrupted', error_summary='进程重启，任务中断', finished_at=now WHERE status='running'`；其 `job_steps.status='running'` 置 `failed`。
2. `status='pending'` 的任务重新入队（不进 retry_of，视为同一任务继续）。
3. 重置 `clusters.collecting=0`，避免刷新永久"复用"死任务。

---

## 8. 前端架构

### 8.1 路由表

| 路径 | 组件 | 布局 | 权限 | 说明 |
| --- | --- | --- | --- | --- |
| `/login` | `views/login/Index.vue` | 空 | — | 独立页 |
| `/setup` | `views/setup/Index.vue` | 空 | — | 仅空库可访问，否则重定向 `/login` |
| `/overview` | `views/overview/Index.vue` | Global | A/O/V | 首页 |
| `/clusters` | `views/cluster/List.vue` | Global | A/O/V | 筛选写 URL query |
| `/clusters/takeover` | `views/cluster/Takeover.vue` | Global | A/O | 分步表单 |
| `/clusters/:id` | → 重定向 `overview` | Cluster | A/O/V | — |
| `/clusters/:id/overview` | `views/cluster/Overview.vue` | Cluster | A/O/V | 基础信息 + 拓扑缩略 |
| `/clusters/:id/topology` | `views/cluster/Topology.vue` | Cluster | A/O/V | 全屏画布 |
| `/clusters/:id/instances` | `views/cluster/Instances.vue` | Cluster | A/O/V | — |
| `/clusters/:id/users` | `views/cluster/TiDBUsers.vue` | Cluster | A/O/V | 写操作按角色隐藏 |
| `/clusters/:id/resource-groups` | `views/cluster/ResourceGroups.vue` | Cluster | A/O/V | 能力不满足时渲染 `Unsupported.vue` |
| `/clusters/:id/sessions` | `views/cluster/Sessions.vue` | Cluster | A/O/V | 同上 |
| `/clusters/:id/params` | `views/cluster/Params.vue` | Cluster | A/O/V | MVP 只读 |
| `/clusters/:id/performance` | `views/cluster/Performance.vue` | Cluster | A/O/V | P1，空态 |
| `/clusters/:id/inspection` | `views/cluster/Inspection.vue` | Cluster | A/O/V | — |
| `/clusters/:id/changes` | `views/cluster/Changes.vue` | Cluster | A/O/V | P1 |
| `/hosts` | `views/host/List.vue` | Global | A/O/V | 支持「按主机/按集群」切换 |
| `/hosts/:id` | `views/host/Detail.vue` | Global | A/O/V | P2 |
| `/platform/users` | `views/platform/Users.vue` | Global | A | — |
| `/platform/roles` | `views/platform/Roles.vue` | Global | A/O/V | 只读矩阵 |
| `/profile` | `views/platform/Profile.vue` | Global | A/O/V | — |
| `/inspection/templates` | `views/inspection/Templates.vue` | Global | A/O/V | P1 |
| `/inspection/reports` | `views/inspection/Reports.vue` | Global | A/O/V | — |
| `/inspection/reports/:id` | `views/inspection/ReportDetail.vue` | Global | A/O/V | — |
| `/tasks` | `views/task/List.vue` | Global | A/O/V | — |
| `/tasks/:id` | `views/task/Detail.vue` | Global | A/O/V | 2s 轮询 |
| `/settings/collection` | `views/settings/Collection.vue` | Global | A（只读 O/V） | — |
| `/settings/credentials` | `views/settings/Credentials.vue` | Global | A | — |
| `/settings/demo` | `views/settings/Demo.vue` | Global | A | P1 |
| `/settings/audit` | `views/settings/Audit.vue` | Global | A | P1 |
| `/settings/about` | `views/settings/About.vue` | Global | A/O/V | P1 |
| `/:pathMatch(.*)*` | `views/error/NotFound.vue` | Global | — | 404 页带返回入口 |

**路由守卫**：无 token → `/login`；`mustChangePassword` → 强制 `/profile`；角色不满足 → 403 页；`/clusters/:id/*` 进入时拉取集群 `capabilities` 存入 `useClusterStore`，用于二级导航显隐。

### 8.2 状态管理（Pinia）

| store | 状态 | 主要 action |
| --- | --- | --- |
| `useAuthStore` | `token`、`user`、`permissions` | `login`、`logout`、`fetchMe`、`hasPerm(action)` |
| `useAppStore` | `mockMode`、`sidebarCollapsed`、`currentLayout`、枚举字典缓存 | `loadMeta`、`toggleSidebar` |
| `useClusterStore` | `currentCluster`、`capabilities`、`featureVisible` | `enterCluster(id)`、`leaveCluster()`、`refreshCaps()` |
| `useTaskStore` | `runningCount`、`recentJobs` | `startPolling()`（30s，页面卸载时停止）、`stopPolling()` |
| `useMetaStore` | `enums`、`capabilityMatrix`、`version` | `load()` 一次性缓存 |

### 8.3 API 层与拦截器（`utils/request.ts`）

```ts
// 伪代码，仅示契约
axios.create({ baseURL: import.meta.env.VITE_API_BASE + '/api/v1', timeout: 20000 })
// 请求：注入 Authorization、X-Request-Id(randomUUID)
// 响应：code===0 → 返回 data；否则 throw BizError(code,message,httpStatus)
// 401/40101 → 清 token → router.replace('/login?redirect=...')
// 40301 → ElMessage.error('无权限执行该操作')
// 40901 → 由调用方自行处理（版本不支持/状态冲突，通常渲染空态页）
// 50201 → 展示采集失败原因，附「重试」按钮
// 50001/网络错误 → 统一 toast，不白屏
```

### 8.4 主题与布局

- 深色侧栏 `#1F2733`（220px，可折叠至 64px），内容区 `#F5F7FA` + 白卡片，主色 `#2563EB`；状态色统一 `success #16A34A` / `warning #D97706` / `danger #DC2626` / `unknown #94A3B8`，一律「色点 + 文字」。
- 顶栏：Logo + 面包屑（集群视角下为「集群 / {name}」+ 集群切换下拉）+ 任务入口角标 + 演示模式标识 + 用户菜单。
- 单集群沉浸式：`ClusterLayout` 替换侧栏菜单为 9 项，顶部常驻集群名/版本/状态/Topbar；点击面包屑「集群」或「返回全局视图」回 `/clusters`。
- 表格：`el-table` 斑马纹可选、操作列 `fixed="right"`；统一 `PageHeader` + `EmptyState`（插图 + 说明 + 主操作）组件。

### 8.5 ECharts 图表清单

| 图表 | 位置 | 类型 | 数据源 |
| --- | --- | --- | --- |
| 集群健康分布 | 总览 | 环形 `pie` | `/overview/health-distribution` |
| 巡检通过率 | 报告详情 | 环形 `pie`（gauge 风格） | `/inspection/reports/:id` |
| QPS / 连接数 / 存储用量 / TiKV 水位 | 集群性能 | 多 `line`，`dataZoom` 可选 | `/clusters/:id/metrics`（P1） |
| 主机资源趋势 | 主机详情 | 迷你 `line` ×3 | `/hosts/:id/metrics`（P1） |
| 任务进度 | 任务详情 | 步骤时间线（`el-steps` + `el-timeline`，非 ECharts） | `/jobs/:id/steps` |

---

## 9. 安全设计

| 面 | 措施 |
| --- | --- |
| 凭据加密 | AES-256-GCM；密钥优先取 `TMP_SECRET`（32 字节，可为任意长度经 SHA-256 派生），缺省时在 `<dataDir>/secret.key` 生成 0600 文件并 **WARN 日志提示固定**；`nonce` 前置拼接后 base64 存 `*_enc`；接口一律返回 `"******"`，仅 `PUT credentials` 接受明文 |
| 密码 | bcrypt Cost 10；策略 ≥8 位且含字母+数字（正则 `[A-Za-z]` + `[0-9]`）；首启创建 `admin/admin123` 并置 `must_change_password=1`（D 决策与 PRD F-07 折中：既满足「开箱即用」又不硬编码在代码中，首次登录强制改密） |
| JWT | HS256，`sub=userId`、`ver=token_version`、`exp`、`iat`、`jti`；密钥由 `TMP_SECRET` 派生（与 AES 派生用不同 salt）；每次请求校验 `ver == platform_users.token_version`，改密/禁用即全端失效；前端 localStorage 存 token，`idle_timeout_min` 由前端计时 + 后端 `exp` 双保险 |
| RBAC | `auth.Action` 常量枚举（约 40 个），`role→set[Action]` 静态矩阵；路由注册时声明 `Require(ActionCreateTiDBUser)`，中间件统一校验；**每个接口独立声明**，不靠前端隐藏；越权 40301 + 写审计 |
| 审计 | 覆盖 PRD 8.4 全部写操作 + 登录/登出/导出/下载；写 `audit_logs`，`detail` JSON 经 `logger.Sanitize` 脱敏（key 命中 `password|secret|token|key` → `***`）；表不加 update/delete 接口，物理上无删除路径 |
| SQL 注入 | 平台元数据库全走 GORM 参数化；对 TiDB 执行的语句**只用占位符 + `?` 参数**（`go-sql-driver` 的 `interpolateParams=false`）；标识符（TiDB 用户名/库名/RG 名）先行 `pkg/sqlsafe.Ident`：正则 `^[A-Za-z0-9_]{1,64}$` + 关键字黑名单 + 反引号包裹，校验失败 40001 |
| 命令注入 | SSH 只执行 `pkg/sshcmd` 中**预定义命令模板**（`cat %s`、`uname -m`、`df -Pm` 等），参数经 `^[A-Za-z0-9._/-]+$` 校验；不接收任意命令；不拼接 `&&`/`|`/`;` |
| 传输 | 服务端不强制 TLS（由前置反代终止），但启动时若检测到非 HTTPS 反代则日志告警；响应头加 `X-Content-Type-Options`、`X-Frame-Options: DENY`、`Referrer-Policy` |
| 日志约束 | 结构化 slog，字段白名单；密码/密钥/token 永不落日志；采集错误信息经脱敏（替换连接串中的 `user:pass@`） |
| 敏感操作 | 移除纳管（需输入集群名）、Kill 会话、删 TiDB 用户、切换演示模式：前端二次确认弹窗 + 后端审计 |

---

## 10. 任务分解（MVP：P0 + 必要 P1）

> 硬约束：每个任务 ≥3 个文件、按模块分层分组、配置文件不拆分、无单文件任务。并行标注见 §10.2。

| 任务 | 名称 | PRD 覆盖 | 涉及文件 | 依赖 | 验收标准 |
| --- | --- | --- | --- | --- | --- |
| **T-01** | 项目骨架与基础设施 | F-70(部分)、NFR 9.3 | `Makefile`、`backend/go.mod`、`backend/cmd/tmp/main.go`、`backend/web/embed.go`、`backend/web/dist/.gitkeep`、`backend/internal/config/{config,version}.go`、`backend/internal/db/{db,migrate}.go`、`backend/internal/api/{router,response,middleware}.go`、`backend/internal/pkg/logger/*`、`frontend/package.json`、`frontend/vite.config.ts`、`frontend/tsconfig.json`、`frontend/index.html` | — | `CGO_ENABLED=0 go build` 通过；启动生成 `tmp.db` 并 AutoMigrate 出 19 张表；`GET /healthz` 200；`GET /` 返回嵌入 index.html（前端未构建时返回占位页不 panic）；`/api/v1/*` 未认证返回 40101 |
| **T-02** | 认证、RBAC 与审计内核 | F-01/02/06/07 | `internal/auth/{jwt,password,rbac,middleware}.go`、`internal/crypto/aesgcm.go`、`internal/service/{user,auth,audit}_service.go`、`internal/api/auth_handler.go`、`internal/repo/{user,audit}_repo.go`、`internal/db/seed.go` | T-01 | 空库首启跳 `/api/v1/setup/status`=false，`POST /setup/init` 建 admin（must_change_password=1）；登录成功签发 JWT，连续 5 次失败锁 10 分钟（42901 + `lockedUntil`）；过期/篡改 token → 40101；viewer 调写接口 → 40301 并落审计；AES-GCM 加解密往返一致 |
| **T-03** | 模型补全 + 平台用户/角色 API | F-03/04/05/06/08 | `internal/model/*.go`（19 个结构体）、`internal/repo/user_repo.go`、`internal/service/user_service.go`、`internal/api/user_handler.go`、`internal/repo/setting_repo.go`、`internal/service/setting_service.go` | T-02 | 全部 model 与 §4.1 DDL 逐字段对齐（含索引 tag）；用户 CRUD/禁用/重置密码可用；最后一个 admin 不可降级（40001）；列表不返回 password_hash；`GET /roles/matrix` 与 PRD 8.4 完全一致 |
| **T-04** | 采集适配层（4 实现 + 归一化） | F-16/17/18/21/22 的采集内核、D-6/D-7 | `internal/collector/{collector,composite,ssh,mysql,pd,mock,mapping,cap}.go`、`internal/pkg/{sshcmd,sqlsafe,retry}/*`、`internal/service/capability_service.go` | T-03 | mock 模式返回 3 套集群完整 Snapshot；真实三通道各自 `Probe` 返回 ok/失败原因与耗时；`Capabilities` 对 6.5/7.1 返回 `supportsResourceGroup=false`；meta.yaml 解析出 instances 且字段映射符合 §6.3；单集群整体超时 15s 生效；无用户输入可注入命令 |
| **T-05** | 任务引擎 + 步骤/日志 + 恢复 | F-59/60/61/62 | `internal/job/{engine,types,handlers,recover}.go`、`internal/repo/job_repo.go`、`internal/service/job_service.go`、`internal/api/job_handler.go`、`internal/model/job*.go` | T-03 | 有界队列 + worker pool；步骤与日志按 §7.3 落库；`GET /jobs/:id/logs?afterSeq` 可增量拉取；重启用 running→interrupted、pending 重新入队；panic 不拖垮 worker |
| **T-06** | 集群接管/列表/详情 + 采集调度 | F-14/15/16/17/18/21/22/23/24/25/26/27 | `internal/service/cluster_service.go`、`internal/repo/{cluster,instance,host,credential}_repo.go`、`internal/api/cluster_handler.go`、`internal/job/handlers.go`（takeover/collect）、`internal/api/scheduler.go` | T-04、T-05 | 连接测试 3 项独立展示、整体 15s；接管提交建异步任务并跳详情；重复集群名 40001；刷新幂等复用进行中任务；拓扑接口节点数 = 实例数；组件版本聚合与不一致提示；连续 3 次失败置 error 且仍可看快照；草稿不计统计 |
| **T-07** | 主机聚合视图 | F-45/46 | `internal/service/host_service.go`、`internal/repo/host_repo.go`、`internal/api/host_handler.go`、`internal/collector/mapping.go`（主机归一） | T-06 | 同机多实例角色合并（如 `PD+TiKV`）；跨集群共享主机列出全部集群；按主机/按集群两种视图数据一致；筛选数与列表 total 一致 |
| **T-08** | TiDB 对象：用户 CRUD + 参数只读 + 会话/RG 只读 | F-33/34/35/42（+F-38/40 只读 P1） | `internal/service/tidbobj_service.go`、`internal/repo/{tidbuser,variable,session,rg}_repo.go`、`internal/api/tidbobj_handler.go`、`internal/job/handlers.go`（create/drop/kill） | T-06 | 用户列表来自 `mysql.user` 真实/快照数据；创建/删除走异步任务 + 审计；root 与连接账号受保护（40301）；<v7.5 的 RG 列返回 null 且接口 409/40901；参数表与 `SHOW GLOBAL VARIABLES` 一致、差异项高亮、可导出 CSV；`PUT variables` 非白名单 40301 |
| **T-09** | 巡检引擎 + 模板 + 报告 | F-50/51(P1)、F-52/54/55/56/57 | `internal/inspect/{engine,checkers,render}.go`、`internal/inspect/templates/default.yaml`、`internal/repo/{inspect,report}_repo.go`、`internal/service/inspect_service.go`、`internal/api/inspect_handler.go`、`internal/pkg/idgen/*` | T-04、T-05、T-08 | 内置 6 类 ≥20 项且每项有判定/建议（seed 幂等）；多集群巡检各出报告；分级计数之和 = 项总数；N/A 与 Unknown 不计入通过率分母；报告含采集时间；MD/HTML 可下载且 HTML 离线可打开；集群移除后报告仍可查 |
| **T-10** | 总览聚合 + 审计查询 + 凭据/设置 API | F-09/11/13(P1)/66/68(P1)/70(P1) + 兼容矩阵 | `internal/service/{overview,setting,credential}_service.go`、`internal/api/{overview,setting}_handler.go`、`internal/db/seed.go`、`internal/api/router.go`（全量路由收口） | T-06、T-09 | 4 张指标卡数字与各列表 total 一致；异常数 >0 可识别；近期任务进行中置顶；凭据接口只返回掩码、更新需重输；审计可筛选、不可删改、CSV 导出；`/settings/about` 返回兼容矩阵 |
| **T-11** | 前端骨架（构建/Layout/路由/axios/登录） | F-01/02/23 的壳 | `frontend/src/main.ts`、`App.vue`、`router/index.ts`、`utils/{request,permission,format,download}.ts`、`stores/{auth,app,cluster,task,meta}.ts`、`layouts/{GlobalLayout,ClusterLayout}.vue`、`styles/*.scss`、`views/login/Index.vue`、`views/setup/Index.vue`、`views/error/{NotFound,Forbidden}.vue`、`api/*.ts`（7 个模块骨架）、`components/{StatusDot,EmptyState,PageHeader,MetricCard}.vue` | T-01（可并行于 T-02~T-10） | `npm run build` 产物落 `backend/web/dist`，单二进制访问可登录；未登录跳登录；401 自动跳登录；侧栏两级切换（全局 7 项 / 集群 9 项）；状态色与 PRD 7.1 一致 |
| **T-12** | 前端页面组一：总览 / 集群列表 / 接管 / 集群概览+拓扑+实例 | F-09/11/14/15/16/17/18/21/22/23/24/25/26/27 | `views/overview/Index.vue`、`views/cluster/{List,Takeover,Overview,Topology,Instances}.vue`、`components/{TopologyCanvas,JobSteps,ConnectionTestPanel}.vue` | T-11、T-06 | 筛选写 URL query 可回退可分享；接管分步表单含实时连接测试；拓扑支持拖拽/缩放/适应屏幕、节点数=实例数、异常节点红色；采集失败行标黄并显示「数据截至」；100 节点拖拽 ≥30fps |
| **T-13** | 前端页面组二：TiDB 对象 / 主机 / 巡检 / 任务 / 设置 | F-33/34/35/38/40/42/45/46/52/54/56/57/59/60/61/62/66 | `views/cluster/{TiDBUsers,Params,Sessions,ResourceGroups,Inspection,Changes}.vue`、`views/host/{List,Detail}.vue`、`views/inspection/{Templates,Reports,ReportDetail}.vue`、`views/task/{List,Detail}.vue`、`views/settings/{Collection,Credentials,Demo,Audit,About}.vue`、`views/platform/{Users,Roles,Profile}.vue` | T-12、T-08、T-09、T-10 | 全部 P0 页面可达且有空态/错误态；任务详情 2s 轮询日志追加、>5000 行滚动流畅、失败步骤标红；参数页「仅看差异」与导出可用；报告 MD/HTML 下载文件名含集群名+时间戳；按角色隐藏入口且越权有 403 提示 |
| **T-14** | 联调、mock 全流程、单二进制构建与冒烟 | 全 P0、G5、NFR 9.3 | `Makefile`、`README.md`、`backend/internal/collector/mock.go`（补齐 3 套数据）、少量跨层修复 | T-13 | `make build` 产出单二进制；`TMP_MOCK=true` 下无真实集群完成「接管→采集→拓扑→用户 CRUD→巡检→报告下载→任务中心」全流程；刷新页面/深链路由不 404；Ctrl-C 重启后 interrupted 任务可见；`/healthz`、`/readyz` 正常 |

### 10.1 并行说明

- **可并行**：`T-04 / T-05 / T-09` 在 T-03 完成后可并行（不同目录、无交叉写）；`T-11` 前端骨架可与 `T-02~T-10` 全程并行。
- **必须串行**：`T-01 → T-02 → T-03 → T-06 → T-08/T-09 → T-10 → T-13 → T-14`。
- 建议排期：T-01~T-03（骨架与安全）→ T-04+T-05 并行 → T-06 → T-07+T-08 并行 → T-09 → T-10 → T-11 起前端 → T-12 → T-13 → T-14。

### 10.2 依赖图

```mermaid
graph LR
  T01["T-01 骨架/配置/DB/路由"] --> T02["T-02 认证·RBAC·审计·加密"]
  T02 --> T03["T-03 模型·平台用户·设置"]
  T03 --> T04["T-04 采集适配层 4 实现"]
  T03 --> T05["T-05 任务引擎+步骤日志"]
  T04 --> T06["T-06 集群接管/列表/详情/调度"]
  T05 --> T06
  T06 --> T07["T-07 主机聚合"]
  T06 --> T08["T-08 TiDB 对象+参数只读"]
  T04 --> T09["T-09 巡检引擎+报告"]
  T05 --> T09
  T08 --> T09
  T06 --> T10["T-10 总览+凭据+审计"]
  T09 --> T10
  T01 --> T11["T-11 前端骨架"]
  T11 --> T12["T-12 页面组一"]
  T06 --> T12
  T12 --> T13["T-13 页面组二"]
  T08 --> T13
  T09 --> T13
  T10 --> T13
  T13 --> T14["T-14 联调·mock·单二进制"]
```

---

## 11. 共享约定

| 项 | 约定 |
| --- | --- |
| Go 包命名 | 全小写无下划线；接口 `Xxxer`/名词；构造器 `NewXxx`；错误变量 `ErrXxx` |
| 文件命名 | 后端 `snake_case.go`（`cluster_service.go`）；前端组件 `PascalCase.vue`、其余 `camelCase.ts` |
| 时间 | 全部 RFC3339（含时区，服务器默认 `Asia/Shanghai`）；DB 存 `DATETIME`；前端 `dayjs` 统一格式化，相对时间仅用于「x 分钟前」 |
| ID | 全表自增 `int64`；对外展示用 `id`；报告/运行另有业务号 `RP/runs`：`RP20261120-0001`、`IR20261120-0001`（`idgen` 按日计数） |
| 状态枚举 | 英文小写 + 下划线：`pending|running|success|failed|canceled|interrupted`、`healthy|degraded|error|unknown`、`up|down|unhealthy|unknown`、`critical|warning|pass|na|unknown`、`active|disabled`、`admin|operator|viewer`、`majority|dr_auto_sync|unknown`、`manual|scheduled` |
| JSON 字段 | 一律 camelCase（`clusterId`、`fetchedAt`、`passRate`），由 struct tag 控制；不使用 `snake_case` |
| 分页 | 请求 `page/pageSize`；响应 `{list,total,page,pageSize}`；`pageSize` 上限 200 |
| 空值 | Go `nil` → JSON `null`；前端展示统一 `—`；列表接口不返回 `password*`、`*_enc` |
| 集群名唯一 | 应用层写入前 `name_lower=strings.ToLower(name)`；移除纳管时 `name_lower += "#removed#"+id`，释放唯一键 |
| mock 契约 | `TMP_MOCK=true` 或 `settings.mode.mock=true` 启用 `MockCollector`；内置 3 套：`mock-prod-a`（v7.5.0/x86_64/DR AUTO SYNC/12 实例/含 TiFlash+TiCDC）、`mock-prod-b`（v6.5.4/x86_64/Majority/8 实例/无 RG 能力）、`mock-test-c`（v8.5.0/aarch64/Majority/5 实例/含 1 个 Down 节点触发降级）。写操作在 mock 下返回成功任务但仅改内存快照，响应统一带 `mock:true` 与「不会影响真实环境」提示；顶栏与总览双处展示模式标识 |
| 日志 | `request_id` 贯穿 handler→service→collector；采集错误带 `cluster_id`、`channel`；密码类字段经 `Sanitize` |
| 事务 | 快照落库（instances + 4 张快照表 + clusters 计数）在**同一事务**内全量替换；失败整体回滚，保留上次成功数据 |
| 前端权限 | `authStore.hasPerm('cluster.takeover')` 控制入口显隐，但**必须与后端 40301 兜底共存** |

---

## 12. 待明确事项（需用户确认）

| # | 问题 | 当前默认处理 | 影响 |
| --- | --- | --- | --- |
| A1 | 首启管理员：PRD F-07 要求「不硬编码弱密码」，而你给的约束是「自动创建 admin/admin123 并强制提示修改」。两者冲突 | 采用 **admin/admin123 + `must_change_password=1` 强制改密**；若你更倾向 PRD，则改为 `/setup` 引导页（T-02 工作量 +0.5 天） | T-02 验收口径 |
| A2 | 集群"告警信息"（F-14 列表列）在 MVP 无 Alertmanager API（Q5）时如何填充 | MVP 返回空：`alertCount=0`，列渲染 `—`；P2 接入时改为真实告警数 | 列表列设计 |
| A3 | F-19 编辑纳管信息若修改了 `tiup_host`/`db_host`，是否允许？（等于换了一台机器） | 允许，但强制置 `collect_status=degraded` 并要求用户确认「需重新采集」，且写审计 | T-06 实现 |
| A4 | 巡检"通过率"分母是否包含 `unknown` | 按 PRD 8.3：不包含（`unknown` 与 `na` 均不计入分母） | T-09 统计口径 |
| A5 | 单实例纳管上限 20 套集群是否需硬限制 | 不硬限制；列表分页 + 采集并发排队自然限流 | 容量设计 |
| A6 | HTML 报告是否需要内嵌样式（完全自包含、可离线） | 需要：单文件内联 `<style>`，图表用纯文本表格（不引外部 JS） | T-09 渲染 |
| A7 | 是否需要 Dockerfile / systemd unit 示例 | 默认提供 `Dockerfile`（`FROM scratch` + 二进制 + CA 证书）与 `systemd/tmp.service` 示例，放 T-14 | 交付形态 |
| A8 | 前端 Element Plus 是否允许引入 `unplugin-vue-components` 做按需加载 | 默认**不引入**（全量引入，牺牲约 200KB gzip 换取构建确定性）；若首屏 <2s 不达标再评估 | 包体积 |

---

**（文档结束）** 共 19 张表、85 个 REST 接口（MVP 约 60 个）、14 个开发任务、4 张 Mermaid 图（分层 / ER / 采集时序 / 任务状态机）+ 1 张依赖图。
