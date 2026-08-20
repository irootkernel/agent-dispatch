package hermeskanban

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// TaskRecord is the typed public task object frozen in the E0-T4
// fixtures (create-response.json, show-response.json, list-response.json).
// Acceptance-relevant members are required: id, status, and created_at
// must be present for a create to count as structured evidence.
type TaskRecord struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Body          *string  `json:"body"`
	Assignee      *string  `json:"assignee"`
	Status        string   `json:"status"`
	Priority      int      `json:"priority"`
	CreatedBy     *string  `json:"created_by"`
	CreatedAt     int64    `json:"created_at"`
	StartedAt     *int64   `json:"started_at"`
	CompletedAt   *int64   `json:"completed_at"`
	Result        *string  `json:"result"`
	Skills        []string `json:"skills"`
	MaxRetries    *int     `json:"max_retries"`
	MutexKey      *string  `json:"mutex_key"`
	WorkspaceKind *string  `json:"workspace_kind"`
	WorkspacePath *string  `json:"workspace_path"`
}

// taskRef matches the public external reference form t_<8 lowercase
// hex> frozen in every E0-T4 fixture.
var taskRef = regexp.MustCompile(`^t_[0-9a-f]{8}$`)

// requiredForAcceptance validates the members the durable-acceptance
// proof depends on (E0-T4 §4: stable id, status, created_at epoch).
func (t TaskRecord) requiredForAcceptance() error {
	if !taskRef.MatchString(t.ID) {
		return fmt.Errorf("task id %q is not the public t_<8 hex> reference form", truncate(t.ID, 40))
	}
	if t.Status == "" {
		return fmt.Errorf("task record carries no status")
	}
	if t.CreatedAt == 0 {
		return fmt.Errorf("task record carries no created_at epoch")
	}
	return nil
}

// showResponse is the `show <task-id> --json` envelope ({"task": {...}}).
type showResponse struct {
	Task TaskRecord `json:"task"`
}

// Public status enum (E0-T4 §6): the closed execution-status vocabulary.
var PublicStatuses = []string{
	"archived", "blocked", "done", "ready", "review",
	"running", "scheduled", "todo", "triage",
}

// Assignee is one entry of `assignees --json` (E0-T4 §7: profiles are
// enumerable with an on_disk existence flag; --assignee is not validated
// at create time, so routes must check profiles this way first).
type Assignee struct {
	Name   string         `json:"name"`
	OnDisk bool           `json:"on_disk"`
	Counts map[string]int `json:"counts"`
}

// decodeCreate parses the create --json single task object.
func decodeCreate(stdout []byte) (TaskRecord, error) {
	var task TaskRecord
	if err := json.Unmarshal(stdout, &task); err != nil {
		return TaskRecord{}, fmt.Errorf("create response is not a task object: %v", truncate(err.Error(), 200))
	}
	if err := task.requiredForAcceptance(); err != nil {
		return TaskRecord{}, err
	}
	return task, nil
}

// decodeShow parses the show --json {"task": {...}} envelope.
func decodeShow(stdout []byte) (TaskRecord, error) {
	var env showResponse
	if err := json.Unmarshal(stdout, &env); err != nil {
		return TaskRecord{}, fmt.Errorf("show response is not a {task} envelope: %v", truncate(err.Error(), 200))
	}
	if env.Task.ID == "" {
		return TaskRecord{}, fmt.Errorf("show response envelope carries no task id")
	}
	return env.Task, nil
}

// decodeList parses the list --json task array.
func decodeList(stdout []byte) ([]TaskRecord, error) {
	var tasks []TaskRecord
	if err := json.Unmarshal(stdout, &tasks); err != nil {
		return nil, fmt.Errorf("list response is not a task array: %v", truncate(err.Error(), 200))
	}
	return tasks, nil
}

// decodeAssignees parses the assignees --json array.
func decodeAssignees(stdout []byte) ([]Assignee, error) {
	var out []Assignee
	if err := json.Unmarshal(stdout, &out); err != nil {
		return nil, fmt.Errorf("assignees response is not an array: %v", truncate(err.Error(), 200))
	}
	return out, nil
}
