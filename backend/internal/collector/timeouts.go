package collector

import (
	"sync"
	"time"

	"github.com/tmp-platform/backend/internal/pkg/sshcmd"
)

// SSH 命令超时的默认值与系统设置允许区间。
//
// 命令超时与建连超时是两个概念：建连慢基本等于网络不通，5s 快速失败即可；
// 而 `tiup cluster display` 在大集群上枚举实例可能要十几秒，因此单给一条
// 命令 30s 的上限，并允许在「系统设置 → 采集配置」里调整。
const (
	// DefaultSSHCommandTimeout 单条远端命令默认执行上限。
	DefaultSSHCommandTimeout = sshcmd.DefaultCommandTimeout
	// MinSSHCommandTimeoutSec 设置项允许的最小秒数。
	MinSSHCommandTimeoutSec = 5
	// MaxSSHCommandTimeoutSec 设置项允许的最大秒数。
	MaxSSHCommandTimeoutSec = 300
	// SSHConnectTimeout SSH 建连超时。
	SSHConnectTimeout = sshcmd.DefaultConnectTimeout
)

// CommandTimeouts 采集命令超时配置。
//
// 进程内共享、可热更新：保存「系统设置」后，对新发起的采集立即生效
// （已在执行中的命令仍按发起时的上限）。
type CommandTimeouts struct {
	mu     sync.RWMutex
	sshCmd time.Duration
}

// NewCommandTimeouts 构造超时配置；sshCmd <= 0 时用默认值 30s。
func NewCommandTimeouts(sshCmd time.Duration) *CommandTimeouts {
	c := &CommandTimeouts{}
	c.SetSSHCommand(sshCmd)
	return c
}

// SSHCommand 单条远端命令执行超时。
func (c *CommandTimeouts) SSHCommand() time.Duration {
	if c == nil {
		return DefaultSSHCommandTimeout
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.sshCmd <= 0 {
		return DefaultSSHCommandTimeout
	}
	return c.sshCmd
}

// SetSSHCommand 更新单条命令超时；<= 0 表示回落默认值。
//
// 合法区间由服务层（系统设置）校验，这里不再夹取，避免单测里
// 用 1~2s 的极短超时验证「超时即失败」时被悄悄放大。
func (c *CommandTimeouts) SetSSHCommand(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sshCmd = d
}

// SSHWindow SSH 通道的整体时间窗。
//
// 一次拓扑采集最多跑三条命令（定位 tiup、display JSON、display 文本），
// 再加一次建连；用它给上下文设上限，让「卡住」的采集能在窗口处结束，
// 而不是把三条命令的上限串起来等满。
func (c *CommandTimeouts) SSHWindow() time.Duration {
	return SSHConnectTimeout + 3*c.SSHCommand()
}
