# TMP · TiDB 管理平台 —— X86 离线部署手册

适用版本：`1.0.0-mvp` ｜ 目标平台：**Linux x86_64（amd64）** ｜ 部署方式：**单机单二进制 + 内置 SQLite**

---

## 1. 这个包为什么能离线跑

| 事项 | 说明 |
| --- | --- |
| 交付形态 | **单个静态二进制**（`bin/tmp`，约 18 MB），`CGO_ENABLED=0` 编译，**无 glibc 依赖**，glibc / musl（Alpine 等）发行版通用 |
| 前端 | Vue 构建产物已 `//go:embed` 打进二进制，**不需要 nginx**，也不需要 Node |
| 元数据库 | 内嵌 **SQLite（纯 Go 驱动）**，不需要单独装 MySQL/PG |
| 运行时依赖 | 无。不需要 Go、Node、JDK、Python |
| 网络依赖 | **无**。安装、启动、日常运行全程不联网 |

> 目标机上不需要安装任何运行时。整个离线包解压即用。

**唯一的外部依赖是「你要接管的 TiDB 集群本身」**：平台的采集需要能访问该集群的 MySQL 端口（默认 4000）与 PD 端口（默认 2379）；如果你选择 SSH 方式读取 TiUP 元数据，还需要能 SSH 到 TiUP 中控机（默认 22）。

---

## 2. 包内容

```
tmp-1.0.0-mvp-linux-amd64/
├── bin/tmp               # 静态二进制（API + 内嵌前端）
├── install.sh            # 一键安装（systemd 托管）
├── uninstall.sh          # 卸载（默认保留数据）
├── start.sh              # 裸跑启动（容器 / 无 systemd）
├── verify.sh             # 部署前环境自检（只读，不改任何东西）
├── tmp.service           # systemd 单元模板
├── tmp.env.example       # 环境变量模板
├── README-DEPLOY.md      # 本文
└── SHA256SUMS            # 校验和
```

**传入离线环境后，先校验完整性并自检环境：**

```bash
sha256sum -c SHA256SUMS
./verify.sh                 # 检查架构、二进制完整性、端口、磁盘、systemd、TiUP 元数据权限
```

---

## 3. 环境要求

| 项 | 最低要求 | 说明 |
| --- | --- | --- |
| CPU 架构 | x86_64 | `uname -m` 应为 `x86_64` |
| 操作系统 | 主流 Linux（CentOS/RHEL 7+、Ubuntu 18.04+、Debian 10+、Alpine 3.15+、麒麟/统信等） | 静态二进制，不挑发行版 |
| 内存 | 1 GB | 平台自身常驻约 50–100 MB，主要取决于纳管集群数与并发采集 |
| 磁盘 | 1 GB 可用 | 数据库是单个 SQLite 文件，百级集群规模通常 < 100 MB |
| 端口 | 8080（可改） | `TMP_ADDR` 可配置 |
| 权限 | root（安装阶段） | 运行阶段用专用低权限用户 |
| 服务管理 | systemd（推荐） | 无 systemd 时用 `start.sh` 裸跑 |
| Shell | `start.sh` 只需 POSIX sh；`install.sh` / `verify.sh` 需 bash | Alpine 等精简系统若无 bash：可直接 `sh ./start.sh` 运行，或 `apk add bash` 后使用安装脚本 |

---

## 4. 三分钟部署（推荐：systemd 托管）

```bash
# 1) 上传并解压
tar -xzf tmp-1.0.0-mvp-linux-amd64.tar.gz
cd tmp-1.0.0-mvp-linux-amd64

# 2) 校验 + 环境自检
sha256sum -c SHA256SUMS
./verify.sh

# 3) 一键安装（默认 /opt/tmp，端口 8080，运行用户 tmp）
sudo ./install.sh
```

安装脚本会完成：创建系统用户 `tmp` → 铺二进制到 `/opt/tmp/bin` → 生成 `/opt/tmp/tmp.env` → 注册并启动 `tmp.service` → 轮询 `/readyz` 确认就绪 → 打印访问地址。

**访问**：`http://<本机IP>:8080`
**初始账号**：`admin / admin123`（首次登录强制改密）

### 常用参数

```bash
sudo ./install.sh --port 9000              # 换端口
sudo ./install.sh --prefix /data/tmp       # 换安装目录
sudo ./install.sh --user tiup              # 用已有用户运行（见 §6.1）
sudo ./install.sh --no-systemd             # 只铺文件，不注册服务
```

---

## 5. 手工部署（无 systemd / 容器 / 需要精细控制）

`start.sh` 是 POSIX sh，**Alpine 等无 bash 的精简系统也能直接运行**：

```bash
tar -xzf tmp-1.0.0-mvp-linux-amd64.tar.gz
cd tmp-1.0.0-mvp-linux-amd64

# 前台运行（数据落在 ./data，数据库自动落在 ./data/tmp.db）
sh ./start.sh

# 或指定端口 + 后台运行
TMP_ADDR=:9000 nohup sh ./start.sh > tmp.log 2>&1 &

# 健康检查
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/readyz
```

> `install.sh` 与 `verify.sh` 需要 bash（用于 useradd / systemctl 等系统管理动作）。
> 若目标机无 bash：优先用 `sh ./start.sh` 裸跑；确需安装脚本则先 `apk add bash`（离线环境请备好包）。

容器内运行示例：

```dockerfile
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY tmp-1.0.0-mvp-linux-amd64/bin/tmp /usr/local/bin/tmp
ENV TMP_ADDR=:8080 TMP_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/tmp"]
```

> 容器部署务必把 `/data` 挂成持久卷 —— 数据库和密钥都在里面（`TMP_DB` 默认跟随 `TMP_DATA_DIR`）。

---

## 6. 接管 TiDB 集群

登录后进入「集群 → 接管集群」，填写「TiUP 地址」。

平台**只通过 SSH** 到该机器执行 `tiup cluster display`（内置纯 Go SSH 客户端，不依赖系统 `ssh` 命令），**没有「本机直读」模式**：即使 TiUP 与平台同机，也要开启本机 sshd 并按 SSH 填写地址。

### 6.1 字段怎么填

| 字段 | 填法 |
| --- | --- |
| TiUP 地址 | TiUP 中控机 IP（同机部署也填该机 IP，不要依赖本地直读） |
| SSH 端口 | 一般 22 |
| SSH 用户 / 密码 或 私钥 | **部署该集群的 TiUP 属主用户**（通常是 `tidb`，root 部署则为 `root`） |
| 部署目录 | 一般留空（自动定位 `$HOME/.tiup/bin/tiup` 与 PATH）；tiup 装在别处时显式填 |

### 6.2 三个必须知道的点

- **SSH 用户必须与部署用户一致**：`tiup` 读的是**登录用户自己**的 `~/.tiup`，用别的账号登录即使 tiup 可用也会报 `cannot find cluster`。
- **不依赖登录 shell 的 PATH**：SSH 非交互会话不读 `~/.bashrc`，平台会自动定位 tiup 绝对路径（远端 PATH → `$HOME/.tiup/bin/tiup` → `<部署目录>/bin/tiup`），不需要在服务器上加软链。
- **display 失败就是失败**：平台直接显示「实例信息获取失败」及原因（含逐级失败原因），不会再用 meta.yaml 或 PD 状态拼一份降级拓扑。
- **超时可调**：单条远端命令默认 30s，大集群 `display` 较慢时在「系统设置 → 采集配置 → SSH 命令超时」调大（5~300s），保存后对新发起的采集立即生效。

### 6.3 网络要求

| 方向 | 端口 | 用途 |
| --- | --- | --- |
| 平台 → TiDB 实例 | 4000（SQL 端口） | 用户/参数/会话/Resource Group、硬件信息 |
| 平台 → PD | 2379 | 集群状态、复制模式、PD 通道采集 |
| 平台 → TiUP 中控机 | 22（SSH） | **必需**：拓扑与实例状态的唯一取数通道 |
| 用户浏览器 → 平台 | 8080 | Web 界面 |

> 平台**不会**主动外联互联网。所有采集都在你指定的集群内网完成。

---

## 7. 配置说明

配置文件：`/opt/tmp/tmp.env`（systemd `EnvironmentFile`，格式为 `KEY=VALUE`，**不要写 export**）。改完执行 `systemctl restart tmp`。

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `TMP_ADDR` | `:8080` | HTTP 监听地址；只想本机访问用 `127.0.0.1:8080` |
| `TMP_DATA_DIR` | `/opt/tmp/data` | 数据目录（`secret.key` 所在） |
| `TMP_DB` | `<TMP_DATA_DIR>/tmp.db` | SQLite 元数据库；**默认跟随数据目录**，一般无需单独设置 |
| `TMP_SECRET` | 空 | 主密钥；空则首次启动生成 `data/secret.key`（0600） |
| `TMP_MOCK` | `false` | `true` 用内置假集群走通全流程（演示/验收） |
| `TMP_GIN_MODE` | `release` | `debug` 输出调试日志 |
| `TMP_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

日志为 JSON，输出到 stdout，由 journald 收集：

```bash
journalctl -u tmp -f              # 实时
journalctl -u tmp -n 200          # 最近 200 行
journalctl -u tmp --since "1 hour ago"
```

---

## 8. 升级与回滚

平台数据在 `/opt/tmp/data`，与二进制分离，**升级不动数据**。

```bash
# --- 升级 ---
systemctl stop tmp
cp /opt/tmp/bin/tmp /opt/tmp/bin/tmp.bak.$(date +%F)   # 备份旧二进制
install -m 0755 ./bin/tmp /opt/tmp/bin/tmp              # 用自己的拷贝，不要 chown 丢失
systemctl start tmp
curl -s http://127.0.0.1:8080/readyz

# --- 回滚 ---
systemctl stop tmp
cp /opt/tmp/bin/tmp.bak.<日期> /opt/tmp/bin/tmp
systemctl start tmp
```

> 数据库迁移是**自动且幂等**的（启动时执行），升级不需要手工改表。但**跨大版本升级前请先备份 `data/`**（见 §9）。

---

## 9. 备份与恢复

**必须备份的两样东西**：`tmp.db`（全部配置与快照）+ `secret.key`（加密集群凭据的密钥）。**两者必须一起备份**——只备份数据库而丢了密钥，已录入的集群凭据无法解密。

```bash
# 备份（服务运行时也安全：SQLite WAL 模式）
systemctl stop tmp    # 最稳妥
tar -czf tmp-backup-$(date +%F).tar.gz -C /opt/tmp data
systemctl start tmp

# 恢复
systemctl stop tmp
rm -rf /opt/tmp/data && tar -xzf tmp-backup-<日期>.tar.gz -C /opt/tmp
chown -R tmp:tmp /opt/tmp/data
systemctl start tmp
```

建议加一条 cron 每日备份并异地留存：

```bash
0 2 * * * tar -czf /backup/tmp-$(date +\%F).tar.gz -C /opt/tmp data
```

---

## 10. 排错

| 现象 | 原因与处理 |
| --- | --- |
| `install.sh` 报「未找到二进制」 | 二进制需位于包内 `bin/tmp` 或与脚本同级的 `tmp-linux-amd64` |
| `/readyz` 一直不 200 | `journalctl -u tmp -n 50` 看日志；常见为端口被占用（`ss -lntp \| grep 8080`）或数据目录不可写 |
| 页面能开但登录 401 | 确认用的是 `admin / admin123`；若曾改密且忘记，见下条 |
| 忘记 admin 密码 | 停服务 → 把 `data/tmp.db` 中 `platform_users` 的 `password_hash` 清空不可行，**推荐**：用备份恢复，或临时新建数据目录起一个新实例重置后再导回（联系维护者协助） |
| 接管集群报「采集失败 / 50201」 | 平台到 4000、2379 端口不通，或被防火墙拦截；用 `nc -z <ip> 4000` 自查 |
| 接管后拓扑为空 / 提示「实例信息获取失败」 | 平台到中控机 22 端口不通，或 SSH 用户不是该集群的 TiUP 属主用户（tiup 看不到别人的 `~/.tiup`）→ 按 §6.2 核对账号；任务日志里会带逐级失败原因 |
| 报「服务 tmp 启动失败」 | `ProtectHome=read-only` 一般无影响（采集不再本地读 `~/.tiup`）；若确需读写家目录，改 unit 里的 `ProtectHome` |
| 磁盘涨得很快 | 检查审计日志与任务日志表体量；平台会保留历史，可定期清理旧任务记录 |
| 二进制无法执行（`Exec format error`） | 架构不符：本包是 x86_64，`uname -m` 需为 `x86_64`；ARM 机器需用 `GOARCH=arm64` 重新打包 |

---

## 11. 安全建议（测试环境也建议照做）

1. **首登必改密**：`admin/admin123` 仅用于首次登录，界面会强制修改。
2. **不要在公网暴露 8080**：测试环境用内网 IP + 防火墙白名单；需要外网访问时前置反向代理并启用 HTTPS。
3. **保护 `secret.key`**：权限 0600，不要进版本库、不要发群；备份加密存放。
4. **最小权限运行**：平台只经 SSH 采集，不需要提升运行用户权限；SSH 侧建议用该集群的 TiUP 属主用户（只读用途）。
5. **收窄监听**：单机自用可设 `TMP_ADDR=127.0.0.1:8080`，通过 SSH 隧道访问。

---

## 12. 交付清单（拿去测试环境前自查）

- [ ] `tmp-<version>-linux-amd64.tar.gz` 已拷贝到目标机（U 盘 / scp / 内网跳板均可）
- [ ] `sha256sum -c SHA256SUMS` 通过
- [ ] `./verify.sh` 自检通过（架构 x86_64、二进制完整、端口空闲）
- [ ] 8080 端口未被占用
- [ ] 平台到 TiDB 集群的 4000 / 2379 网络可达（若现在就想接管真实集群）
- [ ] 平台到 TiUP 中控机 22 端口可达，且该 SSH 账号是集群的 TiUP 属主用户（§6.2）
- [ ] 已规划 `data/` 的备份位置与频率

---

## 附：在构建机上重新打包

工具链齐备的机器上（Go ≥ 1.22、Node ≥ 20 已装，且已完成一次 `make build` 以生成前端产物）：

```bash
VERSION=1.0.1 ./scripts/build-offline.sh                 # 默认 linux/amd64
GOARCH=arm64 VERSION=1.0.1 ./scripts/build-offline.sh    # 交叉编译 ARM64
```

产物：`dist/tmp-<version>-linux-<arch>.tar.gz`。
