CREATE TABLE task_deployment_results (
    task_id VARCHAR(36) NOT NULL,
    attempt_id VARCHAR(36) NOT NULL,
    attempt_number INTEGER NOT NULL,
    result_index INTEGER NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    server_name VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL,
    message TEXT NOT NULL,
    backup_id VARCHAR(128) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, attempt_id, result_index)
);

CREATE INDEX idx_task_deployment_results_task_id ON task_deployment_results(task_id);
