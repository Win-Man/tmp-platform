package service

import "github.com/tmp-platform/backend/internal/job"

// EngineBindable 装配期需要注入任务引擎的服务。
//
// 任务引擎与各 service 之间是构造期循环依赖，只能在 main 里两段式注入
// （先 new service，再 new engine，最后回注）。把「注入 + 自检」收敛成这个
// 接口，main 用一次循环统一完成，避免再出现「某个服务忘了 SetEngine、
// 运行时在 nil engine 上解引用 panic」——Kill 会话曾因此整条接口 500。
type EngineBindable interface {
	SetEngine(*job.Engine)
	// EngineBound 报告任务引擎是否已注入，供装配期自检。
	EngineBound() bool
}
