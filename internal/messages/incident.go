// Package messages contains customer-visible incident text shared by the
// gateway and worker state transitions.
package messages

const (
	RecoverySummary = "CloudMonitor 告警已恢复。"
	FailureSummary  = "RCA 未能完成；请检查网关日志后重试。"
	FailureAction   = "检查网关错误并重试该事件。"
)
