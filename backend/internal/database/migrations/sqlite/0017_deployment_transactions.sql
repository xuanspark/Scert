CREATE TABLE deployment_transactions (
    id TEXT NOT NULL PRIMARY KEY,
    task_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    attempt_number INTEGER NOT NULL,
    result_index INTEGER NOT NULL,
    server_id TEXT NOT NULL,
    server_name TEXT NOT NULL,
    certificate_path TEXT NOT NULL,
    certificate_chain_path TEXT NOT NULL,
    private_key_path TEXT NOT NULL,
    temporary_paths_json TEXT NOT NULL,
    installed_paths_json TEXT NOT NULL,
    backups_json TEXT NOT NULL,
    lock_paths_json TEXT NOT NULL,
    reload_attempted INTEGER NOT NULL DEFAULT 0,
    phase TEXT NOT NULL,
    status TEXT NOT NULL,
    message TEXT NOT NULL,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_deployment_transactions_status ON deployment_transactions(status, updated_at);
CREATE INDEX idx_deployment_transactions_task_id ON deployment_transactions(task_id);
