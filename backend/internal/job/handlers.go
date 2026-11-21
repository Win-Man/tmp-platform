package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/pkg/sqlsafe"
	"github.com/tmp-platform/backend/internal/pkg/sshcmd"
	"github.com/tmp-platform/backend/internal/repo"
)

// registerHandlers 注册全部任务处理函数。
func (e *Engine) registerHandlers() {
	e.handlers[TypeTakeover] = e.handleTakeover
	e.handlers[TypeCollect] = e.handleCollect
	e.handlers[TypeInspect] = e.handleInspect
	e.handlers[TypeSSHCheck] = e.handleSSHCheck
	e.handlers[TypeCreateTiDBUser] = e.handleCreateTiDBUser
	e.handlers[TypeDropTiDBUser] = e.handleDropTiDBUser
	e.handlers[TypeKillSession] = e.handleKillSession
	e.handlers[TypeResetTiDBPassword] = e.handleResetTiDBPassword
	e.handlers[TypeReport] = e.handleReport
}

// ---------------------------------------------------------------------------
// takeover
// ---------------------------------------------------------------------------

// handleTakeover 接管采集：校验 → 三通道探测 → 读取拓扑 → 落库。
func (e *Engine) handleTakeover(ctx context.Context, r *Runner) error {
	var p TakeoverPayload
	if err := decodePayload(r.job.Payload, &p); err != nil {
		return err
	}
	cluster, err := e.deps.Clusters.GetByID(ctx, p.ClusterID)
	if err != nil {
		return fmt.Errorf("集群不存在: %w", err)
	}
	col := e.deps.Collector()
	isMock := e.deps.IsMock != nil && e.deps.IsMock()

	// 步骤 1：校验参数。
	if err := r.Do(1, func() error {
		if strings.TrimSpace(cluster.Name) == "" {
			return errors.New("集群名不能为空")
		}
		if strings.TrimSpace(cluster.TiUPHost) == "" {
			return errors.New("TiUP 主机地址不能为空")
		}
		if cluster.TiUPPort <= 0 || cluster.TiUPPort > 65535 {
			return errors.New("TiUP SSH 端口不合法")
		}
		if strings.TrimSpace(cluster.DBHost) == "" {
			return errors.New("数据库地址不能为空")
		}
		if cluster.DBPort <= 0 || cluster.DBPort > 65535 {
			return errors.New("数据库端口不合法")
		}
		r.Logf(1000, "集群 %s 参数校验通过（TiUP %s:%d，DB %s:%d）",
			cluster.Name, cluster.TiUPHost, cluster.TiUPPort, cluster.DBHost, cluster.DBPort)
		return nil
	}); err != nil {
		return err
	}

	target, err := e.buildTarget(ctx, cluster)
	if err != nil {
		return err
	}

	// 步骤 2~4：三通道连通性（PD 失败不阻塞）。
	if err := r.Do(2, func() error {
		res, perr := col.Probe(ctx, target, []collector.ProbeScope{collector.ScopeSSH})
		if perr != nil {
			return perr
		}
		if len(res) == 0 || !res[0].OK {
			msg := "SSH 连通性检查未通过"
			if len(res) > 0 {
				msg = res[0].Message
			}
			return errors.New(msg)
		}
		r.SetResult("sshLatencyMs", res[0].LatencyMS)
		r.Logf(2000, "SSH 连接成功，耗时 %dms", res[0].LatencyMS)
		return nil
	}); err != nil {
		return err
	}

	if err := r.Do(3, func() error {
		res, perr := col.Probe(ctx, target, []collector.ProbeScope{collector.ScopeMySQL})
		if perr != nil {
			return perr
		}
		if len(res) == 0 || !res[0].OK {
			msg := "数据库连通性检查未通过"
			if len(res) > 0 {
				msg = res[0].Message
			}
			return errors.New(msg)
		}
		r.SetResult("dbLatencyMs", res[0].LatencyMS)
		r.Logf(3000, "数据库连接成功，耗时 %dms", res[0].LatencyMS)
		return nil
	}); err != nil {
		return err
	}

	if err := r.Do(4, func() error {
		if strings.TrimSpace(cluster.PDEndpoint) == "" {
			r.Log(4000, "warn", "未配置 PD 地址，跳过 PD 探测（PD 为建议项，不影响纳管）")
			return nil
		}
		res, perr := col.Probe(ctx, target, []collector.ProbeScope{collector.ScopePD})
		if perr != nil {
			r.Log(4000, "warn", "PD 探测异常：%s（不影响纳管）", perr.Error())
			return nil
		}
		if len(res) == 0 || !res[0].OK {
			msg := "PD 不可达"
			if len(res) > 0 {
				msg = res[0].Message
			}
			r.Log(4000, "warn", "PD 探测未通过：%s（不影响纳管）", msg)
			return nil
		}
		r.Logf(4000, "PD API 可达，耗时 %dms", res[0].LatencyMS)
		return nil
	}); err != nil {
		return err
	}

	// 步骤 5：读取拓扑。
	var snap *collector.Snapshot
	if err := r.Do(5, func() error {
		s, cerr := col.Collect(ctx, target, collector.FullScope())
		if cerr != nil {
			return fmt.Errorf("读取集群拓扑失败: %w", cerr)
		}
		snap = s
		r.Logf(5000, "读取到 %d 个实例、%d 台主机、%d 个账号", len(s.Instances), len(s.Hosts), len(s.Users))
		for k, v := range s.ChanErrors {
			r.Log(5000, "warn", "通道 %s 采集失败：%s", k, v)
		}
		return nil
	}); err != nil {
		return err
	}

	// 步骤 6：写入元数据（同一事务）。
	if err := r.Do(6, func() error {
		if e.deps.Sink == nil {
			return errors.New("快照落库端口未初始化")
		}
		if perr := e.deps.Sink.PersistSnapshot(ctx, cluster.ID, snap); perr != nil {
			return fmt.Errorf("写入元数据失败: %w", perr)
		}
		r.Logf(6000, "元数据写入完成，采集状态 %s", clusterStatusOf(snap))
		return nil
	}); err != nil {
		return err
	}

	// 步骤 7：完成。
	return r.Do(7, func() error {
		if isMock {
			r.Logf(7000, "演示模式：本次接管仅写入本地演示数据，不会影响真实环境")
		} else {
			r.Logf(7000, "接管采集完成")
		}
		return nil
	})
}

// clusterStatusOf 描述快照健康状态（仅日志展示用）。
func clusterStatusOf(snap *collector.Snapshot) string {
	if snap == nil {
		return "unknown"
	}
	if len(snap.ChanErrors) == 0 {
		return "healthy"
	}
	return "degraded"
}

// ---------------------------------------------------------------------------
// collect
// ---------------------------------------------------------------------------

// handleCollect 刷新采集：能力探测 → 三通道采集 → 归一化落库。
func (e *Engine) handleCollect(ctx context.Context, r *Runner) error {
	var p CollectPayload
	if err := decodePayload(r.job.Payload, &p); err != nil {
		return err
	}
	cluster, err := e.deps.Clusters.GetByID(ctx, p.ClusterID)
	if err != nil {
		return fmt.Errorf("集群不存在: %w", err)
	}
	col := e.deps.Collector()
	target, err := e.buildTarget(ctx, cluster)
	if err != nil {
		// 目标构造失败也要落库失败状态，避免 collecting 标记永久占用。
		if e.deps.Sink != nil {
			_ = e.deps.Sink.PersistSnapshot(ctx, cluster.ID, &collector.Snapshot{
				ChanErrors: map[collector.Kind]string{collector.KindSSH: err.Error()},
				FetchedAt:  time.Now(),
			})
		}
		return err
	}

	var snap *collector.Snapshot

	// 步骤 1：能力探测（失败不阻塞，仅影响 UI 显隐）。
	if derr := r.Do(1, func() error {
		caps, cerr := col.Capabilities(ctx, target)
		if cerr != nil {
			r.Log(1000, "warn", "版本能力探测失败：%s（不影响采集）", cerr.Error())
			return nil
		}
		r.Logf(1000, "TiDB 版本 %s，Resource Group 能力=%v", caps.TiDBVersion, caps.SupportsResourceGroup)
		r.SetResult("tidbVersion", caps.TiDBVersion)
		return nil
	}); derr != nil {
		return derr
	}

	// 步骤 2：全量采集（三通道并行）。
	if derr := r.Do(2, func() error {
		s, cerr := col.Collect(ctx, target, collector.FullScope())
		if cerr != nil {
			return fmt.Errorf("采集失败: %w", cerr)
		}
		snap = s
		r.Logf(2000, "SSH 通道：解析到 %d 个实例、%d 台主机", len(s.Instances), len(s.Hosts))
		if msg, bad := s.ChanErrors[collector.KindSSH]; bad {
			r.Log(2000, "warn", "SSH 通道失败：%s", msg)
		}
		return nil
	}); derr != nil {
		if e.deps.Sink != nil && snap == nil {
			_ = e.deps.Sink.PersistSnapshot(ctx, cluster.ID, &collector.Snapshot{
				ChanErrors: map[collector.Kind]string{collector.KindSSH: derr.Error(), collector.KindMySQL: derr.Error()},
				FetchedAt:  time.Now(),
			})
		}
		return derr
	}

	// 步骤 3：MySQL 对象。
	if derr := r.Do(3, func() error {
		r.Logf(3000, "MySQL 通道：账号 %d 个、参数 %d 项、会话 %d 个、Resource Group %d 个",
			len(snap.Users), len(snap.Variables), len(snap.Sessions), len(snap.RGs))
		if msg, bad := snap.ChanErrors[collector.KindMySQL]; bad {
			r.Log(3000, "warn", "MySQL 通道失败：%s", msg)
		}
		return nil
	}); derr != nil {
		return derr
	}

	// 步骤 4：PD 状态。
	if derr := r.Do(4, func() error {
		if msg, bad := snap.ChanErrors[collector.KindPD]; bad {
			r.Log(4000, "warn", "PD 通道失败：%s（不单独降级）", msg)
		} else {
			r.Logf(4000, "PD 通道：TiKV store %d 个，部署模式 %s", len(snap.Stores), snap.DeployModeHint)
		}
		return nil
	}); derr != nil {
		return derr
	}

	// 步骤 5：归一化落库。
	return r.Do(5, func() error {
		if e.deps.Sink == nil {
			return errors.New("快照落库端口未初始化")
		}
		if perr := e.deps.Sink.PersistSnapshot(ctx, cluster.ID, snap); perr != nil {
			return fmt.Errorf("归一化落库失败: %w", perr)
		}
		r.Logf(5000, "采集结果已落库，状态 %s", clusterStatusOf(snap))
		return nil
	})
}

// ---------------------------------------------------------------------------
// inspect
// ---------------------------------------------------------------------------

// handleInspect 巡检：加载模板 → 采集快照 → 逐项判定 → 汇总 → 生成报告。
func (e *Engine) handleInspect(ctx context.Context, r *Runner) error {
	var p InspectPayload
	if err := decodePayload(r.job.Payload, &p); err != nil {
		return err
	}
	if e.deps.Inspector == nil {
		return errors.New("巡检执行端口未初始化")
	}
	if p.RunID <= 0 {
		return errors.New("缺少巡检运行 ID")
	}

	// 声明式步骤 1/4/5 由 Runner 自身推进，2/3 由巡检执行端口回调推进。
	// 这里先把 1 标记为运行中，其余阶段由 hooks 控制。
	hooks := RunHooks{
		OnPhaseStart: func(phase int) {
			switch phase {
			case PhaseLoadTemplate:
				r.Logf(1000, "开始加载巡检模板")
			case PhaseCollect:
				r.doStart(2)
			case PhaseJudge:
				r.doStart(3)
			case PhaseSummarize:
				r.doStart(4)
			case PhaseReport:
				r.doStart(5)
			}
		},
		OnPhaseEnd: func(phase int, err error) {
			switch phase {
			case PhaseLoadTemplate:
				r.doEnd(1, err)
			case PhaseCollect:
				r.doEnd(2, err)
			case PhaseJudge:
				r.doEnd(3, err)
			case PhaseSummarize:
				r.doEnd(4, err)
			case PhaseReport:
				r.doEnd(5, err)
			}
		},
		OnItemDone: func(done, total int, itemName, level string) {
			r.Logf(3000, "判定进度 %d/%d：%s → %s", done, total, itemName, level)
			if total > 0 {
				r.SetProgress(20 + int(float64(done)/float64(total)*55))
			}
		},
	}
	return e.deps.Inspector.ExecuteRun(ctx, p.RunID, hooks)
}

// doStart 把声明式步骤置为 running（供 hooks 使用）。
func (r *Runner) doStart(stepIndex int) {
	seq := stepIndex * stepSeqBase
	step, ok := r.stepBySeq(seq)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.eng.deps.Repo.UpdateStep(ctx, step.ID, map[string]any{
		"status":     model.StepStatusRunning,
		"started_at": time.Now(),
	})
	r.mu.Lock()
	r.currentStep = step.Name
	r.mu.Unlock()
	r.Flush()
}

// doEnd 把声明式步骤置为终态（供 hooks 使用）。
func (r *Runner) doEnd(stepIndex int, err error) {
	seq := stepIndex * stepSeqBase
	step, ok := r.stepBySeq(seq)
	if !ok {
		return
	}
	fields := map[string]any{"finished_at": time.Now()}
	if err != nil {
		fields["status"] = model.StepStatusFailed
		fields["error"] = err.Error()
		r.Log(seq, "error", "步骤「%s」失败：%s", step.Name, err.Error())
	} else {
		fields["status"] = model.StepStatusSuccess
		fields["progress"] = 100
		r.completeStep()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.eng.deps.Repo.UpdateStep(ctx, step.ID, fields)
	r.Flush()
}

// ---------------------------------------------------------------------------
// ssh_check
// ---------------------------------------------------------------------------

// handleSSHCheck 主机 SSH 连通性检测。
func (e *Engine) handleSSHCheck(ctx context.Context, r *Runner) error {
	var p SSHCheckPayload
	if err := decodePayload(r.job.Payload, &p); err != nil {
		return err
	}
	if len(p.HostIDs) == 0 {
		return errors.New("未指定主机")
	}

	var hosts []*model.Host
	if err := r.Do(1, func() error {
		for _, id := range p.HostIDs {
			h, err := e.deps.Hosts.GetByID(ctx, id)
			if err != nil {
				r.Log(1000, "warn", "主机 %d 不存在，已跳过", id)
				continue
			}
			hosts = append(hosts, h)
		}
		if len(hosts) == 0 {
			return errors.New("没有可检测的主机")
		}
		r.Logf(1000, "共 %d 台主机待检测", len(hosts))
		return nil
	}); err != nil {
		return err
	}

	if err := r.Do(2, func() error {
		for i, h := range hosts {
			host := h
			subErr := r.RunSubStep(2, fmt.Sprintf("检测 %s", host.IP), func() error {
				if e.deps.IsMock != nil && e.deps.IsMock() {
					r.Logf(2000, "%s SSH 检测成功（演示模式）", host.IP)
					_ = e.deps.Hosts.UpdateSSHStatus(ctx, host.ID, model.SSHStatusOK, "")
					return nil
				}
				if derr := e.probeHostSSH(ctx, host); derr != nil {
					_ = e.deps.Hosts.UpdateSSHStatus(ctx, host.ID, model.SSHStatusFail, derr.Error())
					return derr
				}
				_ = e.deps.Hosts.UpdateSSHStatus(ctx, host.ID, model.SSHStatusOK, "")
				r.Logf(2000, "%s SSH 检测成功", host.IP)
				return nil
			})
			if subErr != nil {
				// 单台失败不中断批量检测。
				r.Log(2000, "warn", "%s SSH 检测失败：%s", host.IP, subErr.Error())
			}
			r.SetProgress(10 + int(float64(i+1)/float64(len(hosts))*80))
		}
		return nil
	}); err != nil {
		return err
	}

	return r.Do(3, func() error {
		r.Logf(3000, "SSH 状态已回写 %d 台主机", len(hosts))
		return nil
	})
}

// probeHostSSH 用所属集群的 OS 凭据探测单台主机。
func (e *Engine) probeHostSSH(ctx context.Context, h *model.Host) error {
	cluster, err := e.clusterOfHost(ctx, h.ID)
	if err != nil {
		return fmt.Errorf("未找到主机所属集群: %w", err)
	}
	target, err := e.buildTarget(ctx, cluster)
	if err != nil {
		return err
	}
	user := h.OSUser
	if user == "" {
		user = target.TiUPUser
	}
	runner, derr := sshcmd.Dial(sshcmd.DialOptions{
		Host:       h.IP,
		Port:       22,
		User:       user,
		Password:   target.TiUPPassword,
		PrivateKey: target.TiUPKey,
		Timeout:    5 * time.Second,
	})
	if derr != nil {
		return derr
	}
	defer func() { _ = runner.Close() }()
	_, rerr := runner.RunTemplate("arch")
	return rerr
}

// clusterOfHost 找到承载该主机的第一个集群。
func (e *Engine) clusterOfHost(ctx context.Context, hostID int64) (*model.Cluster, error) {
	insts, err := e.deps.Instances.InstancesByHost(ctx, hostID)
	if err != nil {
		return nil, err
	}
	for _, inst := range insts {
		c, cerr := e.deps.Clusters.GetByID(ctx, inst.ClusterID)
		if cerr == nil {
			return c, nil
		}
	}
	return nil, errors.New("主机未被任何集群承载")
}

// ---------------------------------------------------------------------------
// create_tidb_user
// ---------------------------------------------------------------------------

// handleCreateTiDBUser 创建 TiDB 用户（标识符白名单 + 参数化执行）。
func (e *Engine) handleCreateTiDBUser(ctx context.Context, r *Runner) error {
	var p CreateUserPayload
	if err := decodePayload(r.job.Payload, &p); err != nil {
		return err
	}
	cluster, err := e.deps.Clusters.GetByID(ctx, p.ClusterID)
	if err != nil {
		return fmt.Errorf("集群不存在: %w", err)
	}
	col := e.deps.Collector()

	var identQuoted, hostQuoted string
	if derr := r.Do(1, func() error {
		q, qerr := sqlsafe.Ident(p.UserName)
		if qerr != nil {
			return apperr.BadRequest("用户名不合法：" + qerr.Error())
		}
		identQuoted = q
		hq, herr := sqlsafe.Host(p.Host)
		if herr != nil {
			return apperr.BadRequest("Host 不合法：" + herr.Error())
		}
		hostQuoted = hq
		r.Logf(1000, "标识符校验通过：%s@%s", p.UserName, p.Host)
		return nil
	}); derr != nil {
		return derr
	}

	if derr := r.Do(2, func() error {
		n, cerr := e.deps.TiDBObj.CountUsersByName(ctx, cluster.ID, p.UserName, p.Host)
		if cerr != nil {
			r.Log(2000, "warn", "重名预检失败，继续执行：%s", cerr.Error())
			return nil
		}
		if n > 0 {
			return apperr.Conflict(fmt.Sprintf("用户 %s@%s 已存在", p.UserName, p.Host))
		}
		r.Logf(2000, "未发现同名账号")
		return nil
	}); derr != nil {
		return derr
	}

	target, terr := e.buildTarget(ctx, cluster)
	if terr != nil {
		return terr
	}

	if derr := r.Do(3, func() error {
		stmt := fmt.Sprintf("CREATE USER %s@%s IDENTIFIED BY %s", identQuoted, hostQuoted, quoteLiteral(p.Password))
		if xerr := col.Exec(ctx, target, collector.Statement{SQL: stmt}); xerr != nil {
			return fmt.Errorf("创建用户失败: %w", xerr)
		}
		r.Logf(3000, "用户 %s@%s 创建成功", p.UserName, p.Host)
		return nil
	}); derr != nil {
		return derr
	}

	if derr := r.Do(4, func() error {
		if len(p.Grants) == 0 {
			r.Logf(4000, "未指定初始授权，跳过")
			return nil
		}
		for _, g := range p.Grants {
			priv, perr := sqlsafe.Privilege(g)
			if perr != nil {
				r.Log(4000, "warn", "跳过非法权限 %s：%s", g, perr.Error())
				continue
			}
			stmt := fmt.Sprintf("GRANT %s ON *.* TO %s@%s", priv, identQuoted, hostQuoted)
			if xerr := col.Exec(ctx, target, collector.Statement{SQL: stmt}); xerr != nil {
				return fmt.Errorf("授予 %s 失败: %w", priv, xerr)
			}
			r.Logf(4000, "已授予 %s", priv)
		}
		return nil
	}); derr != nil {
		return derr
	}

	return r.Do(5, func() error {
		rg := p.ResourceGroup
		if rg == "" {
			rg = "default"
		}
		u := &model.TiDBUser{
			ClusterID:       cluster.ID,
			UserName:        p.UserName,
			Host:            p.Host,
			AuthPlugin:      "mysql_native_password",
			AccountLocked:   "N",
			PasswordExpired: "N",
			ResourceGroup:   rg,
			FetchedAt:       time.Now(),
		}
		if uerr := e.deps.TiDBObj.UpsertUser(ctx, u); uerr != nil {
			r.Log(5000, "warn", "用户快照刷新失败：%s", uerr.Error())
			return nil
		}
		r.SetResult("userName", p.UserName)
		r.Logf(5000, "用户快照已刷新")
		return nil
	})
}

// handleDropTiDBUser 删除 TiDB 用户（root 与当前连接账号受保护）。
func (e *Engine) handleDropTiDBUser(ctx context.Context, r *Runner) error {
	var p DropUserPayload
	if err := decodePayload(r.job.Payload, &p); err != nil {
		return err
	}
	cluster, err := e.deps.Clusters.GetByID(ctx, p.ClusterID)
	if err != nil {
		return fmt.Errorf("集群不存在: %w", err)
	}
	col := e.deps.Collector()

	var identQuoted, hostQuoted string
	if derr := r.Do(1, func() error {
		if strings.EqualFold(p.UserName, "root") {
			return apperr.Forbidden("root 为受保护账号，不允许通过平台删除")
		}
		if strings.EqualFold(p.UserName, cluster.DBUser) && (p.Host == "%" || p.Host == cluster.DBHost) {
			return apperr.Forbidden("该账号为平台当前连接账号，不允许删除")
		}
		q, qerr := sqlsafe.Ident(p.UserName)
		if qerr != nil {
			return apperr.BadRequest("用户名不合法：" + qerr.Error())
		}
		hq, herr := sqlsafe.Host(p.Host)
		if herr != nil {
			return apperr.BadRequest("Host 不合法：" + herr.Error())
		}
		identQuoted, hostQuoted = q, hq
		r.Logf(1000, "保护账号校验通过：%s@%s", p.UserName, p.Host)
		return nil
	}); derr != nil {
		return derr
	}

	target, terr := e.buildTarget(ctx, cluster)
	if terr != nil {
		return terr
	}

	if derr := r.Do(2, func() error {
		stmt := fmt.Sprintf("DROP USER %s@%s", identQuoted, hostQuoted)
		if xerr := col.Exec(ctx, target, collector.Statement{SQL: stmt}); xerr != nil {
			return fmt.Errorf("删除用户失败: %w", xerr)
		}
		r.Logf(2000, "用户 %s@%s 已删除", p.UserName, p.Host)
		return nil
	}); derr != nil {
		return derr
	}

	return r.Do(3, func() error {
		if p.SnapshotID > 0 {
			if derr := e.deps.TiDBObj.DeleteUserRow(ctx, cluster.ID, p.SnapshotID); derr != nil {
				r.Log(3000, "warn", "用户快照删除失败：%s", derr.Error())
				return nil
			}
		}
		r.Logf(3000, "用户快照已刷新")
		return nil
	})
}

// handleResetTiDBPassword 重置 TiDB 用户密码。
func (e *Engine) handleResetTiDBPassword(ctx context.Context, r *Runner) error {
	var p ResetPasswordPayload
	if err := decodePayload(r.job.Payload, &p); err != nil {
		return err
	}
	cluster, err := e.deps.Clusters.GetByID(ctx, p.ClusterID)
	if err != nil {
		return fmt.Errorf("集群不存在: %w", err)
	}
	col := e.deps.Collector()

	var identQuoted, hostQuoted string
	if derr := r.Do(1, func() error {
		q, qerr := sqlsafe.Ident(p.UserName)
		if qerr != nil {
			return apperr.BadRequest("用户名不合法：" + qerr.Error())
		}
		hq, herr := sqlsafe.Host(p.Host)
		if herr != nil {
			return apperr.BadRequest("Host 不合法：" + herr.Error())
		}
		if len(p.Password) < 8 {
			return apperr.BadRequest("新密码长度需不少于 8 位")
		}
		identQuoted, hostQuoted = q, hq
		r.Logf(1000, "账号 %s@%s 校验通过", p.UserName, p.Host)
		return nil
	}); derr != nil {
		return derr
	}

	target, terr := e.buildTarget(ctx, cluster)
	if terr != nil {
		return terr
	}

	if derr := r.Do(2, func() error {
		stmt := fmt.Sprintf("ALTER USER %s@%s IDENTIFIED BY %s", identQuoted, hostQuoted, quoteLiteral(p.Password))
		if xerr := col.Exec(ctx, target, collector.Statement{SQL: stmt}); xerr != nil {
			return fmt.Errorf("重置密码失败: %w", xerr)
		}
		r.Logf(2000, "密码已重置，客户端需重新连接")
		return nil
	}); derr != nil {
		return derr
	}

	return r.Do(3, func() error {
		r.Logf(3000, "审计记录已由接口层写入")
		return nil
	})
}

// handleKillSession 批量 Kill 会话。
func (e *Engine) handleKillSession(ctx context.Context, r *Runner) error {
	var p KillSessionPayload
	if err := decodePayload(r.job.Payload, &p); err != nil {
		return err
	}
	cluster, err := e.deps.Clusters.GetByID(ctx, p.ClusterID)
	if err != nil {
		return fmt.Errorf("集群不存在: %w", err)
	}
	col := e.deps.Collector()
	if len(p.SessionIDs) == 0 {
		return errors.New("未指定会话")
	}

	var sessions []model.Session
	if derr := r.Do(1, func() error {
		list, qerr := e.deps.TiDBObj.GetSessionsByIDs(ctx, cluster.ID, p.SessionIDs)
		if qerr != nil {
			return fmt.Errorf("拉取会话失败: %w", qerr)
		}
		sessions = list
		r.Logf(1000, "共 %d 个会话待 Kill", len(sessions))
		return nil
	}); derr != nil {
		return derr
	}

	target, terr := e.buildTarget(ctx, cluster)
	if terr != nil {
		return terr
	}

	successCount := 0
	if derr := r.Do(2, func() error {
		for i, s := range sessions {
			sess := s
			subErr := r.RunSubStep(2, fmt.Sprintf("KILL %d", sess.SessionID), func() error {
				stmt := fmt.Sprintf("KILL %d", sess.SessionID)
				if xerr := col.Exec(ctx, target, collector.Statement{SQL: stmt}); xerr != nil {
					return xerr
				}
				r.Logf(2000, "会话 %d（%s）已 Kill", sess.SessionID, sess.UserName)
				return nil
			})
			if subErr != nil {
				r.Log(2000, "warn", "会话 %d Kill 失败：%s", sess.SessionID, subErr.Error())
			} else {
				successCount++
			}
			r.SetProgress(5 + int(float64(i+1)/float64(len(sessions))*90))
		}
		return nil
	}); derr != nil {
		return derr
	}

	r.SetResult("killed", successCount)
	r.SetResult("total", len(sessions))
	return nil
}

// handleReport 重新生成巡检报告（失败重试）。
func (e *Engine) handleReport(ctx context.Context, r *Runner) error {
	var p ReportPayload
	if err := decodePayload(r.job.Payload, &p); err != nil {
		return err
	}
	if e.deps.Inspector == nil {
		return errors.New("巡检执行端口未初始化")
	}
	if err := r.Do(1, func() error {
		if _, err := e.deps.Inspect.GetRun(ctx, p.RunID); err != nil {
			return fmt.Errorf("巡检运行不存在: %w", err)
		}
		r.Logf(1000, "已加载巡检运行 #%d", p.RunID)
		return nil
	}); err != nil {
		return err
	}
	hooks := RunHooks{
		OnPhaseStart: func(phase int) {
			if phase == PhaseLoadTemplate {
				r.doStart(2)
			}
		},
		OnPhaseEnd: func(phase int, err error) {
			switch phase {
			case PhaseLoadTemplate:
				r.doEnd(2, err)
			case PhaseReport:
				r.doEnd(3, err)
			}
		},
	}
	return e.deps.Inspector.RegenerateReportWithHooks(ctx, p.RunID, hooks)
}

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------

// buildTarget 由集群配置 + 解密凭据构造采集目标。
func (e *Engine) buildTarget(ctx context.Context, cluster *model.Cluster) (*collector.Target, error) {
	cred, err := e.deps.Creds.GetByClusterID(ctx, cluster.ID)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return nil, fmt.Errorf("读取凭据失败: %w", err)
	}
	return collector.NewTarget(cluster, cred, e.deps.Cipher), nil
}

// decodePayload 解析任务入参。
func decodePayload(raw string, out any) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("任务入参为空")
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("任务入参格式错误: %w", err)
	}
	return nil
}

// quoteLiteral 把字符串包装为 SQL 单引号字面量并转义。
//
// 与 sqlsafe 配合使用：标识符走白名单，值走转义。
func quoteLiteral(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "'", "''", "\x00", "")
	return "'" + r.Replace(s) + "'"
}
