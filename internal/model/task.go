package model

import "time"

type Task struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Prompt    string    `json:"prompt"`
	Schedule  string    `json:"schedule,omitempty"` // cron 表达式，空=一次性任务
	RunAt     time.Time `json:"run_at,omitempty"`   // 一次性任务指定时间
	WorkDir   string    `json:"work_dir"`
	Status    string    `json:"status"` // active / paused / done
	CreatedAt time.Time `json:"created_at"`
	NextRunAt time.Time `json:"next_run_at,omitempty"`
	LastRunAt time.Time `json:"last_run_at,omitempty"`
}

type ExecutionLog struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"task_id"`
	Output    string    `json:"output"`
	Status    string    `json:"status"` // success / failed / running
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}
