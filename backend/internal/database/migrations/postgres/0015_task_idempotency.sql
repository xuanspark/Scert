CREATE TABLE task_idempotency (
    domain_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    task_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (domain_id, idempotency_key),
    UNIQUE (task_id)
);

INSERT INTO task_idempotency (domain_id, idempotency_key, task_id, created_at)
SELECT domain_id, idempotency_key, MIN(id), MIN(created_at)
FROM tasks
WHERE idempotency_key <> ''
GROUP BY domain_id, idempotency_key;
