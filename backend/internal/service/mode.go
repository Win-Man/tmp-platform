// Package service 领域服务层：业务规则、事务边界、跨聚合编排。
//
// 依赖方向：api → service → repo/collector/job/inspect，禁止反向依赖。
package service

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/repo"
)

// ModeManager 采集模式管理：在真实采集器与演示采集器之间切换。
//
// TMP_MOCK=true 时强制演示模式（不可被 settings 覆盖）。
type ModeManager struct {
	mu         sync.RWMutex
	mock       bool
	forcedMock bool
	real       collector.Collector
	mockC      collector.Collector
	timeouts   *collector.CommandTimeouts
}

// NewModeManager 构造模式管理器。
func NewModeManager(forcedMock bool) *ModeManager {
	timeouts := collector.NewCommandTimeouts(collector.DefaultSSHCommandTimeout)
	m := &ModeManager{
		forcedMock: forcedMock,
		mock:       forcedMock,
		timeouts:   timeouts,
		real:       collector.NewCompositeCollector(timeouts),
		mockC:      collector.NewMockCollector(),
	}
	return m
}

// Timeouts 返回采集超时配置。
//
// 系统设置保存后通过它热更新，真实采集器持有同一份指针，无需重建。
func (m *ModeManager) Timeouts() *collector.CommandTimeouts { return m.timeouts }

// Current 返回当前模式下的采集器。
func (m *ModeManager) Current() collector.Collector {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.mock {
		return m.mockC
	}
	return m.real
}

// IsMock 当前是否演示模式。
func (m *ModeManager) IsMock() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mock
}

// ForcedMock 是否被环境变量强制演示模式。
func (m *ModeManager) ForcedMock() bool { return m.forcedMock }

// SetMock 切换模式；强制模式下忽略切换请求。
func (m *ModeManager) SetMock(enabled bool) bool {
	if m.forcedMock {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mock = enabled
	return true
}

// LoadFromSettings 启动时按 settings 初始化模式与采集超时。
func (m *ModeManager) LoadFromSettings(ctx context.Context, settings *repo.SettingRepo) error {
	// SSH 命令超时与模式无关，强制演示模式时也要加载（关闭演示后立即生效）。
	if v, err := settings.Get(ctx, repo.SettingSSHTimeoutSec); err == nil {
		if sec, cerr := strconv.Atoi(v); cerr == nil && sec > 0 {
			m.timeouts.SetSSHCommand(time.Duration(sec) * time.Second)
		}
	}

	if m.forcedMock {
		return nil
	}
	v, err := settings.Get(ctx, repo.SettingModeMock)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.mock = v == "true" || v == "1"
	m.mu.Unlock()
	return nil
}

// DataSource 返回数据源标识（前端展示用）。
func (m *ModeManager) DataSource() string {
	if m.IsMock() {
		return "内置演示数据（3 套假集群）"
	}
	return "被纳管 TiDB 集群"
}
