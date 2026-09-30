CREATE TABLE deployment_transactions (
    id VARCHAR(128) NOT NULL PRIMARY KEY,
    task_id VARCHAR(36) NOT NULL,
    attempt_id VARCHAR(36) NOT NULL,
    attempt_number INTEGER NOT NULL,
    result_index INTEGER NOT NULL,
    server_id VARCHAR(36) NOT NULL,
    server_name VARCHAR(255) NOT NULL,
    certificate_path TEXT NOT NULL,
    certificate_chain_path TEXT NOT NULL,
    private_key_path TEXT NOT NULL,
    temporary_paths_json TEXT NOT NULL,
    installed_paths_json TEXT NOT NULL,
    backups_json TEXT NOT NULL,
    lock_paths_json TEXT NOT NULL,
    reload_attempted INTEGER NOT NULL DEFAULT 0,
    phase VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    message TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_deployment_transactions_status ON deployment_transactions(status, updated_at);
CREATE INDEX idx_deployment_transactions_task_id ON deployment_transactions(task_id);
