ALTER TABLE tasks ADD COLUMN stage VARCHAR(32) NOT NULL DEFAULT 'queued';
ALTER TABLE tasks ADD COLUMN operator_id VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN certificate_version_id VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN idempotency_key VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP;
CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_idempotency ON tasks(idempotency_key);
CREATE TABLE IF NOT EXISTS task_attempts (
    id VARCHAR(36) NOT NULL PRIMARY KEY,
    task_id VARCHAR(36) NOT NULL,
    attempt_number INT NOT NULL,
    stage VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL,
    message TEXT NOT NULL,
    started_at DATETIME NULL,
    finished_at DATETIME NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uq_task_attempt (task_id, attempt_number),
    INDEX idx_task_attempts_task_id (task_id)
);