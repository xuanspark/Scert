CREATE TABLE task_deployment_results (
    task_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    attempt_number INTEGER NOT NULL,
    result_index INTEGER NOT NULL,
    server_id TEXT NOT NULL,
    server_name TEXT NOT NULL,
    status TEXT NOT NULL,
    message TEXT NOT NULL,
    backup_id TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, attempt_id, result_index)
);

CREATE INDEX idx_task_deployment_results_task_id ON task_deployment_results(task_id);
