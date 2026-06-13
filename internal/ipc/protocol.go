package ipc

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
	RunAt    string `json:"run_at,omitempty"`    // RFC3339
	WorkDir  string `json:"work_dir,omitempty"`
}

type TaskIDParams struct {
	ID string `json:"id"`
}

type ListLogsParams struct {
	TaskID string `json:"task_id"`
	Last   bool   `json:"last,omitempty"`
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
	Running    bool   `json:"running"`
	PID        int    `json:"pid,omitempty"`
	TaskCount  int    `json:"task_count"`
	ActiveCount int   `json:"active_count"`
	Uptime     string `json:"uptime,omitempty"`
}

// Method names
const (
	MethodTaskCreate  = "task.create"
	MethodTaskList    = "task.list"
	MethodTaskGet     = "task.get"
	MethodTaskDelete  = "task.delete"
	MethodTaskPause   = "task.pause"
	MethodTaskResume  = "task.resume"
	MethodTaskRun     = "task.run"
	MethodTaskLogs    = "task.logs"
	MethodDaemonStatus = "daemon.status"
)
