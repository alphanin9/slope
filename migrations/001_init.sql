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
