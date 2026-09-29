CREATE TABLE task_deployment_results (
    task_id VARCHAR(36) NOT NULL,
    attempt_id VARCHAR(36) NOT NULL,
    attempt_number INT NOT NULL,
    result_index INT NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    server_name VARCHAR(255) NOT NULL,
    status VARCHAR(32) NOT NULL,
    message TEXT NOT NULL,
    backup_id VARCHAR(128) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, attempt_id, result_index),
    INDEX idx_task_deployment_results_task_id (task_id)
);
