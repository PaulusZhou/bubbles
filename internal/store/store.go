package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/pauluszhou/bubbles/internal/model"
)

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			prompt      TEXT NOT NULL,
			schedule    TEXT DEFAULT '',
			run_at      DATETIME DEFAULT NULL,
			work_dir    TEXT DEFAULT '',
			status      TEXT DEFAULT 'active',
			created_at  DATETIME NOT NULL,
			next_run_at DATETIME DEFAULT NULL,
			last_run_at DATETIME DEFAULT NULL
		);
		CREATE TABLE IF NOT EXISTS execution_logs (
			id         TEXT PRIMARY KEY,
			task_id    TEXT NOT NULL,
			output     TEXT DEFAULT '',
			status     TEXT DEFAULT 'running',
			started_at DATETIME NOT NULL,
			ended_at   DATETIME DEFAULT NULL,
			FOREIGN KEY (task_id) REFERENCES tasks(id)
		);
		CREATE INDEX IF NOT EXISTS idx_logs_task_id ON execution_logs(task_id);
	`)
	return err
}

// --- Task CRUD ---

func (s *Store) CreateTask(t *model.Task) error {
	_, err := s.db.Exec(`
		INSERT INTO tasks (id, name, prompt, schedule, run_at, work_dir, status, created_at, next_run_at, last_run_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Prompt, t.Schedule, nullTime(t.RunAt), t.WorkDir, t.Status,
		t.CreatedAt, nullTime(t.NextRunAt), nullTime(t.LastRunAt),
	)
	return err
}

func (s *Store) GetTask(id string) (*model.Task, error) {
	row := s.db.QueryRow(`
		SELECT id, name, prompt, schedule, run_at, work_dir, status, created_at, next_run_at, last_run_at
		FROM tasks WHERE id = ?`, id)
	return scanTask(row)
}

func (s *Store) ListTasks() ([]model.Task, error) {
	rows, err := s.db.Query(`
		SELECT id, name, prompt, schedule, run_at, work_dir, status, created_at, next_run_at, last_run_at
		FROM tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		t, err := scanTaskRow(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

func (s *Store) ListActiveTasks() ([]model.Task, error) {
	rows, err := s.db.Query(`
		SELECT id, name, prompt, schedule, run_at, work_dir, status, created_at, next_run_at, last_run_at
		FROM tasks WHERE status = 'active' ORDER BY next_run_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		t, err := scanTaskRow(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, *t)
	}
	return tasks, rows.Err()
}

func (s *Store) UpdateTaskStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, status, id)
	return err
}

func (s *Store) UpdateTaskNextRun(id string, nextRun time.Time) error {
	_, err := s.db.Exec(`UPDATE tasks SET next_run_at = ? WHERE id = ?`, nextRun, id)
	return err
}

func (s *Store) UpdateTaskLastRun(id string, lastRun time.Time) error {
	_, err := s.db.Exec(`UPDATE tasks SET last_run_at = ? WHERE id = ?`, lastRun, id)
	return err
}

func (s *Store) DeleteTask(id string) error {
	// 先删除关联的执行日志
	_, err := s.db.Exec(`DELETE FROM execution_logs WHERE task_id = ?`, id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	return err
}

// --- ExecutionLog CRUD ---

func (s *Store) CreateExecutionLog(e *model.ExecutionLog) error {
	_, err := s.db.Exec(`
		INSERT INTO execution_logs (id, task_id, output, status, started_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		e.ID, e.TaskID, e.Output, e.Status, e.StartedAt, nullTime(e.EndedAt),
	)
	return err
}

func (s *Store) UpdateExecutionLog(id, output, status string, endedAt time.Time) error {
	_, err := s.db.Exec(`
		UPDATE execution_logs SET output = ?, status = ?, ended_at = ? WHERE id = ?`,
		output, status, endedAt, id,
	)
	return err
}

func (s *Store) ListExecutionLogs(taskID string) ([]model.ExecutionLog, error) {
	rows, err := s.db.Query(`
		SELECT id, task_id, output, status, started_at, ended_at
		FROM execution_logs WHERE task_id = ? ORDER BY started_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []model.ExecutionLog
	for rows.Next() {
		var e model.ExecutionLog
		var runAt sql.NullTime
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Output, &e.Status, &e.StartedAt, &runAt); err != nil {
			return nil, err
		}
		if runAt.Valid {
			e.EndedAt = runAt.Time
		}
		logs = append(logs, e)
	}
	return logs, rows.Err()
}

func (s *Store) GetLastExecutionLog(taskID string) (*model.ExecutionLog, error) {
	row := s.db.QueryRow(`
		SELECT id, task_id, output, status, started_at, ended_at
		FROM execution_logs WHERE task_id = ? ORDER BY started_at DESC LIMIT 1`, taskID)
	var e model.ExecutionLog
	var endedAt sql.NullTime
	if err := row.Scan(&e.ID, &e.TaskID, &e.Output, &e.Status, &e.StartedAt, &endedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if endedAt.Valid {
		e.EndedAt = endedAt.Time
	}
	return &e, nil
}

// --- helpers ---

func scanTask(row *sql.Row) (*model.Task, error) {
	var t model.Task
	var runAt, nextRun, lastRun sql.NullTime
	err := row.Scan(&t.ID, &t.Name, &t.Prompt, &t.Schedule, &runAt, &t.WorkDir,
		&t.Status, &t.CreatedAt, &nextRun, &lastRun)
	if err != nil {
		return nil, err
	}
	if runAt.Valid {
		t.RunAt = runAt.Time
	}
	if nextRun.Valid {
		t.NextRunAt = nextRun.Time
	}
	if lastRun.Valid {
		t.LastRunAt = lastRun.Time
	}
	return &t, nil
}

type scannable interface {
	Scan(dest ...interface{}) error
}

func scanTaskRow(row scannable) (*model.Task, error) {
	var t model.Task
	var runAt, nextRun, lastRun sql.NullTime
	err := row.Scan(&t.ID, &t.Name, &t.Prompt, &t.Schedule, &runAt, &t.WorkDir,
		&t.Status, &t.CreatedAt, &nextRun, &lastRun)
	if err != nil {
		return nil, err
	}
	if runAt.Valid {
		t.RunAt = runAt.Time
	}
	if nextRun.Valid {
		t.NextRunAt = nextRun.Time
	}
	if lastRun.Valid {
		t.LastRunAt = lastRun.Time
	}
	return &t, nil
}

func nullTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}
