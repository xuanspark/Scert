ALTER TABLE tasks ADD COLUMN stage TEXT NOT NULL DEFAULT 'queued';
ALTER TABLE tasks ADD COLUMN operator_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN certificate_version_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;
CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_idempotency ON tasks(idempotency_key);
CREATE TABLE IF NOT EXISTS task_attempts (
    id TEXT NOT NULL PRIMARY KEY,
    task_id TEXT NOT NULL,
    attempt_number INTEGER NOT NULL,
    stage TEXT NOT NULL,
    status TEXT NOT NULL,
    message TEXT NOT NULL,
    started_at DATETIME NULL,
    finished_at DATETIME NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (task_id, attempt_number)
);
CREATE INDEX IF NOT EXISTS idx_task_attempts_task_id ON task_attempts(task_id);