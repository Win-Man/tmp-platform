package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/tmp-platform/backend/internal/config"
	"github.com/tmp-platform/backend/internal/repo"
)

// sshTimeoutInput 构造一份其余字段均合法的采集配置，供 SSH 超时用例复用。
func sshTimeoutInput(sec int) CollectionSettings {
	return CollectionSettings{
		IntervalSec:        300,
		TimeoutSec:         10,
		Concurrency:        4,
		Retry:              1,
		SSHTimeoutSec:      sec,
		ThresholdCPUPct:    80,
		ThresholdDiskPct:   85,
		ClusterErrorStreak: 3,
	}
}

// TestUpdateCollection_SSHTimeoutRange 断言 SSH 命令超时的设置区间为 5~300 秒。
//
// 校验发生在写库之前，因此这里不需要初始化 repo（非法值一定提前返回）。
func TestUpdateCollection_SSHTimeoutRange(t *testing.T) {
	s := &SettingService{}
	for _, sec := range []int{-1, 0, 4, 301, 3600} {
		_, err := s.UpdateCollection(context.Background(), sshTimeoutInput(sec), nil)
		if err == nil || !strings.Contains(err.Error(), "SSH 命令超时") {
			t.Fatalf("SSHTimeoutSec=%d 应被拒绝，实际: %v", sec, err)
		}
	}
}

// TestUpdateCollection_SSHTimeoutPersistAndApply 断言保存后落库并热更新采集器。
func TestUpdateCollection_SSHTimeoutPersistAndApply(t *testing.T) {
	gdb := svcTestDB(t)
	settingRepo := repo.NewSettingRepo(gdb)
	mode := NewModeManager(false)
	s := NewSettingService(settingRepo, mode, &config.Config{})

	got, err := s.UpdateCollection(context.Background(), sshTimeoutInput(45), nil)
	if err != nil {
		t.Fatalf("保存失败: %v", err)
	}
	if got.SSHTimeoutSec != 45 {
		t.Fatalf("返回的 sshTimeoutSec = %d, want 45", got.SSHTimeoutSec)
	}
	// 落库：换一个服务实例回读仍为 45（值来自 settings 表）。
	reload, err := NewSettingService(settingRepo, mode, &config.Config{}).GetCollection(context.Background())
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if reload.SSHTimeoutSec != 45 {
		t.Fatalf("回读 sshTimeoutSec = %d, want 45", reload.SSHTimeoutSec)
	}
	// 热更新：真实采集器持有同一份 CommandTimeouts，下次采集即用新上限。
	if d := mode.Timeouts().SSHCommand(); d != 45*time.Second {
		t.Fatalf("采集器超时 = %v, want 45s", d)
	}
}

// TestGetCollection_SSHTimeoutDefault 未保存过时回落到默认 30s。
func TestGetCollection_SSHTimeoutDefault(t *testing.T) {
	gdb := svcTestDB(t)
	mode := NewModeManager(false)
	s := NewSettingService(repo.NewSettingRepo(gdb), mode, &config.Config{})
	got, err := s.GetCollection(context.Background())
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if got.SSHTimeoutSec != 30 {
		t.Fatalf("默认 sshTimeoutSec = %d, want 30", got.SSHTimeoutSec)
	}
	if d := mode.Timeouts().SSHCommand(); d != 30*time.Second {
		t.Fatalf("采集器默认超时 = %v, want 30s", d)
	}
}
