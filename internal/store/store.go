package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"slope/internal/config"
	"slope/internal/model"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(schemaSQL)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`ALTER TABLE machines ADD COLUMN guest_port INTEGER NOT NULL DEFAULT 9000`)
	if err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	return nil
}

func (s *Store) UpsertMachines(ctx context.Context, machines []config.MachineConfig) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range machines {
		if m.GuestPort == 0 {
			m.GuestPort = 9000
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO machines(name, platform, locked, snapshot, guest_endpoint, guest_port, last_heartbeat)
VALUES(?, 'windows', 0, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET platform='windows', snapshot=excluded.snapshot, guest_endpoint=excluded.guest_endpoint, guest_port=excluded.guest_port`,
			cleanPersist(m.Name), cleanPersist(m.Snapshot), cleanPersist(m.GuestEndpoint), m.GuestPort, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) CreateTask(ctx context.Context, t model.Task) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO tasks(id, submitted_at, status, sample_ref, vm_name, timeout_sec, error_message)
VALUES(?, ?, ?, ?, ?, ?, ?)`, cleanPersist(t.ID), fmtTime(t.SubmittedAt), t.Status, cleanPersist(t.SampleRef), cleanPersist(t.VMName), t.TimeoutSec, cleanPersist(t.ErrorMessage))
	return err
}

func (s *Store) GetTask(ctx context.Context, id string) (*model.Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, submitted_at, started_at, finished_at, status, sample_ref, vm_name, timeout_sec, error_message FROM tasks WHERE id=?`, id)
	return scanTask(row)
}

func (s *Store) ListScreenshots(ctx context.Context, taskID string) ([]model.Screenshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, task_id, seq, kind, path, captured_at, width, height FROM screenshots WHERE task_id=? ORDER BY seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Screenshot
	for rows.Next() {
		var ss model.Screenshot
		var captured string
		if err := rows.Scan(&ss.ID, &ss.TaskID, &ss.Seq, &ss.Kind, &ss.Path, &captured, &ss.Width, &ss.Height); err != nil {
			return nil, err
		}
		ss.CapturedAt, _ = time.Parse(time.RFC3339Nano, captured)
		out = append(out, ss)
	}
	return out, rows.Err()
}

func (s *Store) AddScreenshot(ctx context.Context, ss model.Screenshot) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO screenshots(task_id, seq, kind, path, captured_at, width, height) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		cleanPersist(ss.TaskID), ss.Seq, ss.Kind, cleanPersist(ss.Path), fmtTime(ss.CapturedAt), ss.Width, ss.Height)
	return err
}

func (s *Store) NextScreenshotSeq(ctx context.Context, taskID string) (int, error) {
	var seq sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(seq) FROM screenshots WHERE task_id=?`, taskID).Scan(&seq)
	if err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 1, nil
	}
	return int(seq.Int64) + 1, nil
}

func (s *Store) AddEvent(ctx context.Context, taskID, level, message string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO task_events(task_id, created_at, level, message) VALUES(?, ?, ?, ?)`,
		cleanPersist(taskID), fmtTime(time.Now().UTC()), cleanPersist(level), cleanPersist(message))
	return err
}

func (s *Store) ClaimNextTask(ctx context.Context) (*model.Task, *model.Machine, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `SELECT id, submitted_at, started_at, finished_at, status, sample_ref, vm_name, timeout_sec, error_message
FROM tasks WHERE status='pending' ORDER BY submitted_at LIMIT 1`)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	mrow := tx.QueryRowContext(ctx, `SELECT name, platform, locked, snapshot, guest_endpoint, guest_port, last_heartbeat FROM machines WHERE name=? AND platform='windows' AND locked=0`, task.VMName)
	machine, err := scanMachine(mrow)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	now := fmtTime(time.Now().UTC())
	res, err := tx.ExecContext(ctx, `UPDATE machines SET locked=1 WHERE name=? AND locked=0`, machine.Name)
	if err != nil {
		return nil, nil, err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, nil, nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET status='running', started_at=? WHERE id=? AND status='pending'`, now, task.ID)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	started, _ := time.Parse(time.RFC3339Nano, now)
	task.Status = model.StatusRunning
	task.StartedAt = &started
	machine.Locked = true
	return task, machine, nil
}

func (s *Store) FinishTask(ctx context.Context, taskID string, status model.TaskStatus, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET status=?, finished_at=?, error_message=? WHERE id=?`, status, fmtTime(time.Now().UTC()), cleanPersist(message), cleanPersist(taskID))
	return err
}

func (s *Store) ReleaseMachine(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE machines SET locked=0, last_heartbeat=? WHERE name=?`, fmtTime(time.Now().UTC()), name)
	return err
}

func (s *Store) Machine(ctx context.Context, name string) (*model.Machine, error) {
	return scanMachine(s.db.QueryRowContext(ctx, `SELECT name, platform, locked, snapshot, guest_endpoint, guest_port, last_heartbeat FROM machines WHERE name=?`, name))
}

type rowScanner interface{ Scan(dest ...any) error }

func scanTask(row rowScanner) (*model.Task, error) {
	var t model.Task
	var submitted string
	var started, finished sql.NullString
	if err := row.Scan(&t.ID, &submitted, &started, &finished, &t.Status, &t.SampleRef, &t.VMName, &t.TimeoutSec, &t.ErrorMessage); err != nil {
		return nil, err
	}
	t.SubmittedAt, _ = time.Parse(time.RFC3339Nano, submitted)
	if started.Valid {
		v, _ := time.Parse(time.RFC3339Nano, started.String)
		t.StartedAt = &v
	}
	if finished.Valid {
		v, _ := time.Parse(time.RFC3339Nano, finished.String)
		t.FinishedAt = &v
	}
	return &t, nil
}

func scanMachine(row rowScanner) (*model.Machine, error) {
	var m model.Machine
	var locked int
	var heartbeat string
	if err := row.Scan(&m.Name, &m.Platform, &locked, &m.Snapshot, &m.GuestEndpoint, &m.GuestPort, &heartbeat); err != nil {
		return nil, err
	}
	m.Locked = locked != 0
	if heartbeat != "" {
		v, _ := time.Parse(time.RFC3339Nano, heartbeat)
		m.LastHeartbeat = &v
	}
	return &m, nil
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func cleanPersist(v string) string {
	v = strings.TrimSpace(v)
	v = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, v)
	if len(v) > 2048 {
		return v[:2048]
	}
	return v
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  submitted_at TEXT NOT NULL,
  started_at TEXT,
  finished_at TEXT,
  status TEXT NOT NULL CHECK(status IN ('pending','running','completed','failed','timeout')),
  sample_ref TEXT NOT NULL,
  vm_name TEXT NOT NULL,
  timeout_sec INTEGER NOT NULL,
  error_message TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS machines (
  name TEXT PRIMARY KEY,
  platform TEXT NOT NULL CHECK(platform = 'windows'),
  locked INTEGER NOT NULL DEFAULT 0,
  snapshot TEXT NOT NULL,
  guest_endpoint TEXT NOT NULL,
  guest_port INTEGER NOT NULL DEFAULT 9000,
  last_heartbeat TEXT
);

CREATE TABLE IF NOT EXISTS task_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  level TEXT NOT NULL,
  message TEXT NOT NULL,
  FOREIGN KEY(task_id) REFERENCES tasks(id)
);

CREATE TABLE IF NOT EXISTS screenshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('periodic','final')),
  path TEXT NOT NULL,
  captured_at TEXT NOT NULL,
  width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0,
  UNIQUE(task_id, seq),
  FOREIGN KEY(task_id) REFERENCES tasks(id)
);

CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status, submitted_at);
CREATE INDEX IF NOT EXISTS idx_screenshots_task_seq ON screenshots(task_id, seq);
`
