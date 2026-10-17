package ipc

import "encoding/json"

// JSON-RPC request/response over Unix Socket

type Request struct {
	Method string      `json:"method"`
	Params interface{} `json:"params,omitempty"`
}

type Response struct {
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// --- Params types ---

type CreateTaskParams struct {
	Name     string `json:"name"`
	Prompt   string `json:"prompt"`
	Schedule string `json:"schedule,omitempty"`
	RunAt    string `json:"run_at,omitempty"` // RFC3339
}

type TaskIDParams struct {
	ID string `json:"id"`
}

type ListLogsParams struct {
	TaskID string `json:"task_id"`
	Last   bool   `json:"last,omitempty"`
}

type SessionParams struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path,omitempty"` // export target (session.export)
}

// --- Result types ---

type TaskResult struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Schedule  string `json:"schedule,omitempty"`
	NextRunAt string `json:"next_run_at,omitempty"`
	LastRunAt string `json:"last_run_at,omitempty"`
}

type ExecutionLogResult struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id"`
	Output    string `json:"output"`
	Status    string `json:"status"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`
}

type DaemonStatusResult struct {
	Running     bool   `json:"running"`
	PID         int    `json:"pid,omitempty"`
	TaskCount   int    `json:"task_count"`
	ActiveCount int    `json:"active_count"`
	Uptime      string `json:"uptime,omitempty"`
}

type SessionStatsResult struct {
	SessionID string          `json:"session_id"`
	Stats     json.RawMessage `json:"stats"`
}

type SessionExportResult struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
}

type FeishuNotifyParams struct {
	ChatID string `json:"chat_id,omitempty"` // falls back to the configured default chat
	Text   string `json:"text"`
}

type FeishuNotifyResult struct {
	Delivered bool `json:"delivered"`
}

// Method names
const (
	MethodTaskCreate    = "task.create"
	MethodTaskList      = "task.list"
	MethodTaskGet       = "task.get"
	MethodTaskDelete    = "task.delete"
	MethodTaskPause     = "task.pause"
	MethodTaskResume    = "task.resume"
	MethodTaskRun       = "task.run"
	MethodTaskLogs      = "task.logs"
	MethodDaemonStatus  = "daemon.status"
	MethodSessionStats  = "session.stats"
	MethodSessionExport = "session.export"
	MethodFeishuNotify  = "feishu.notify"
)
