CREATE TABLE task_validation_results (
    task_id VARCHAR(36) NOT NULL,
    attempt_id VARCHAR(36) NOT NULL,
    attempt_number INT NOT NULL,
    validation_json MEDIUMTEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (task_id, attempt_id)
);
