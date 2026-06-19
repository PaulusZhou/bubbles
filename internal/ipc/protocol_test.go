package ipc

import (
	"encoding/json"
	"testing"
)

func TestRequest_JSON(t *testing.T) {
	req := Request{
		Method: "task.create",
		Params: CreateTaskParams{
			Name:   "test",
			Prompt: "hello",
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Method != "task.create" {
		t.Errorf("Method = %q, want %q", decoded.Method, "task.create")
	}
}

func TestRequest_NoParams(t *testing.T) {
	req := Request{Method: "task.list"}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Should not have "params" key when nil
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if _, ok := raw["params"]; ok {
		t.Error("expected no 'params' key when nil")
	}
}

func TestResponse_Success(t *testing.T) {
	resp := Response{
		Result: TaskResult{
			ID:     "task_1",
			Name:   "test",
			Status: "active",
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Error != "" {
		t.Errorf("unexpected error: %s", decoded.Error)
	}
}

func TestResponse_Error(t *testing.T) {
	resp := Response{
		Error: "task not found",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Error != "task not found" {
		t.Errorf("Error = %q, want %q", decoded.Error, "task not found")
	}

	// Result should be nil/omitted
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if _, ok := raw["result"]; ok {
		t.Error("expected no 'result' key when error is set")
	}
}

func TestCreateTaskParams_JSON(t *testing.T) {
	tests := []struct {
		name   string
		params CreateTaskParams
	}{
		{
			name: "full params",
			params: CreateTaskParams{
				Name:     "test",
				Prompt:   "hello",
				Schedule: "0 9 * * *",
				RunAt:    "2026-06-15T14:30:00Z",
			},
		},
		{
			name: "minimal params",
			params: CreateTaskParams{
				Name:   "test",
				Prompt: "hello",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.params)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}

			var decoded CreateTaskParams
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}

			if decoded.Name != tt.params.Name {
				t.Errorf("Name = %q, want %q", decoded.Name, tt.params.Name)
			}
			if decoded.Prompt != tt.params.Prompt {
				t.Errorf("Prompt = %q, want %q", decoded.Prompt, tt.params.Prompt)
			}
			if decoded.Schedule != tt.params.Schedule {
				t.Errorf("Schedule = %q, want %q", decoded.Schedule, tt.params.Schedule)
			}
		})
	}
}

func TestCreateTaskParams_OmitEmpty(t *testing.T) {
	params := CreateTaskParams{
		Name:   "test",
		Prompt: "hello",
	}

	data, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	if _, ok := raw["schedule"]; ok {
		t.Error("expected 'schedule' to be omitted when empty")
	}
	if _, ok := raw["run_at"]; ok {
		t.Error("expected 'run_at' to be omitted when empty")
	}
}

func TestTaskIDParams_JSON(t *testing.T) {
	params := TaskIDParams{ID: "task_20260101000000"}
	data, _ := json.Marshal(params)

	var decoded TaskIDParams
	json.Unmarshal(data, &decoded)

	if decoded.ID != "task_20260101000000" {
		t.Errorf("ID = %q, want %q", decoded.ID, "task_20260101000000")
	}
}

func TestListLogsParams_JSON(t *testing.T) {
	tests := []struct {
		name   string
		params ListLogsParams
	}{
		{name: "with last", params: ListLogsParams{TaskID: "task_1", Last: true}},
		{name: "without last", params: ListLogsParams{TaskID: "task_1"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, _ := json.Marshal(tt.params)
			var decoded ListLogsParams
			json.Unmarshal(data, &decoded)

			if decoded.TaskID != tt.params.TaskID {
				t.Errorf("TaskID = %q, want %q", decoded.TaskID, tt.params.TaskID)
			}
			if decoded.Last != tt.params.Last {
				t.Errorf("Last = %v, want %v", decoded.Last, tt.params.Last)
			}
		})
	}
}

func TestTaskResult_JSON(t *testing.T) {
	result := TaskResult{
		ID:        "task_1",
		Name:      "test",
		Status:    "active",
		Schedule:  "0 9 * * *",
		NextRunAt: "2026-06-15T09:00:00Z",
		LastRunAt: "2026-06-14T09:00:00Z",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded TaskResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.ID != result.ID {
		t.Errorf("ID = %q, want %q", decoded.ID, result.ID)
	}
	if decoded.Schedule != result.Schedule {
		t.Errorf("Schedule = %q, want %q", decoded.Schedule, result.Schedule)
	}
}

func TestExecutionLogResult_JSON(t *testing.T) {
	result := ExecutionLogResult{
		ID:        "log_1",
		TaskID:    "task_1",
		Output:    "some output with\nnewlines",
		Status:    "success",
		StartedAt: "2026-06-15T09:00:00Z",
		EndedAt:   "2026-06-15T09:05:00Z",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded ExecutionLogResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Output != result.Output {
		t.Errorf("Output = %q, want %q", decoded.Output, result.Output)
	}
}

func TestDaemonStatusResult_JSON(t *testing.T) {
	result := DaemonStatusResult{
		Running:     true,
		PID:         12345,
		TaskCount:   10,
		ActiveCount: 3,
		Uptime:      "2h30m",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded DaemonStatusResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.PID != 12345 {
		t.Errorf("PID = %d, want %d", decoded.PID, 12345)
	}
	if decoded.Uptime != "2h30m" {
		t.Errorf("Uptime = %q, want %q", decoded.Uptime, "2h30m")
	}
}

func TestMethodConstants(t *testing.T) {
	methods := map[string]string{
		MethodTaskCreate:   "task.create",
		MethodTaskList:     "task.list",
		MethodTaskGet:      "task.get",
		MethodTaskDelete:   "task.delete",
		MethodTaskPause:    "task.pause",
		MethodTaskResume:   "task.resume",
		MethodTaskRun:      "task.run",
		MethodTaskLogs:     "task.logs",
		MethodDaemonStatus: "daemon.status",
	}

	for constVal, expected := range methods {
		if constVal != expected {
			t.Errorf("method constant = %q, want %q", constVal, expected)
		}
	}
}

func TestResponse_FullRoundTrip(t *testing.T) {
	// Simulate a full request -> response cycle
	reqJSON := `{"method":"task.create","params":{"name":"test","prompt":"hello"}}`

	var req Request
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		t.Fatalf("Unmarshal request: %v", err)
	}

	if req.Method != "task.create" {
		t.Errorf("Method = %q, want %q", req.Method, "task.create")
	}

	// Marshal params back
	rawParams, _ := json.Marshal(req.Params)
	var createParams CreateTaskParams
	if err := json.Unmarshal(rawParams, &createParams); err != nil {
		t.Fatalf("Unmarshal params: %v", err)
	}

	if createParams.Name != "test" {
		t.Errorf("Name = %q, want %q", createParams.Name, "test")
	}

	// Build response
	resp := Response{
		Result: TaskResult{
			ID:     "task_20260101000000",
			Name:   createParams.Name,
			Status: "active",
		},
	}

	respData, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal response: %v", err)
	}

	var decodedResp Response
	if err := json.Unmarshal(respData, &decodedResp); err != nil {
		t.Fatalf("Unmarshal response: %v", err)
	}

	if decodedResp.Error != "" {
		t.Errorf("unexpected error: %s", decodedResp.Error)
	}
}

// --- Edge cases ---

func TestRequest_UnknownMethod(t *testing.T) {
	reqJSON := `{"method":"unknown.method","params":{}}`
	var req Request
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if req.Method != "unknown.method" {
		t.Errorf("Method = %q, want %q", req.Method, "unknown.method")
	}
}

func TestResponse_EmptyResult(t *testing.T) {
	resp := Response{}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Both result and error should be omitted
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if _, ok := raw["result"]; ok {
		t.Error("expected 'result' to be omitted when nil")
	}
	if _, ok := raw["error"]; ok {
		t.Error("expected 'error' to be omitted when empty")
	}
}

func TestCreateTaskParams_Unicode(t *testing.T) {
	params := CreateTaskParams{
		Name:   "中文任务 🚀",
		Prompt: "请帮我分析代码性能问题",
	}

	data, _ := json.Marshal(params)
	var decoded CreateTaskParams
	json.Unmarshal(data, &decoded)

	if decoded.Name != params.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, params.Name)
	}
	if decoded.Prompt != params.Prompt {
		t.Errorf("Prompt = %q, want %q", decoded.Prompt, params.Prompt)
	}
}
