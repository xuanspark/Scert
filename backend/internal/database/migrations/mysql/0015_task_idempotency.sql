CREATE TABLE task_idempotency (
    domain_id VARCHAR(36) NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    task_id VARCHAR(36) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (domain_id, idempotency_key),
    UNIQUE KEY uq_task_idempotency_task (task_id)
);

INSERT INTO task_idempotency (domain_id, idempotency_key, task_id, created_at)
SELECT domain_id, idempotency_key, MIN(id), MIN(created_at)
FROM tasks
WHERE idempotency_key <> ''
GROUP BY domain_id, idempotency_key;
