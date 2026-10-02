CREATE TABLE task_validation_results (
    task_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    attempt_number INTEGER NOT NULL,
    validation_json TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, attempt_id)
);
