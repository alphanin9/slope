package model

import "time"

type TaskStatus string

const (
	StatusPending   TaskStatus = "pending"
	StatusRunning   TaskStatus = "running"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusTimeout   TaskStatus = "timeout"
)

type ScreenshotKind string

const (
	ShotPeriodic ScreenshotKind = "periodic"
	ShotFinal    ScreenshotKind = "final"
)

type Task struct {
	ID           string     `json:"id"`
	SubmittedAt  time.Time  `json:"submitted_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Status       TaskStatus `json:"status"`
	SampleRef    string     `json:"sample_ref"`
	VMName       string     `json:"vm_name"`
	TimeoutSec   int        `json:"timeout_sec"`
	ErrorMessage string     `json:"error_message,omitempty"`
}

type Screenshot struct {
	ID         int64          `json:"id"`
	TaskID     string         `json:"task_id"`
	Seq        int            `json:"seq"`
	Kind       ScreenshotKind `json:"kind"`
	Path       string         `json:"path"`
	CapturedAt time.Time      `json:"captured_at"`
	Width      int            `json:"width,omitempty"`
	Height     int            `json:"height,omitempty"`
}

type Machine struct {
	Name          string     `json:"name"`
	Platform      string     `json:"platform"`
	Locked        bool       `json:"locked"`
	Snapshot      string     `json:"snapshot"`
	GuestEndpoint string     `json:"guest_endpoint"`
	GuestPort     int        `json:"guest_port"`
	LastHeartbeat *time.Time `json:"last_heartbeat,omitempty"`
}
