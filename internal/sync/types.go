package sync

import "time"

type RunStatus struct {
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt time.Time      `json:"finished_at"`
	Error      string         `json:"error,omitempty"`
	Sources    []SourceStatus `json:"sources"`
}

type SourceStatus struct {
	Name      string `json:"name"`
	Action    string `json:"action"`
	Entries   int    `json:"entries"`
	Hash      string `json:"hash,omitempty"`
	RouteID   string `json:"route_id,omitempty"`
	Error     string `json:"error,omitempty"`
	DryRun    bool   `json:"dry_run"`
	Backup    string `json:"backup,omitempty"`
	Unchanged bool   `json:"unchanged"`
	Added     int    `json:"added,omitempty"`
	Removed   int    `json:"removed,omitempty"`
}
