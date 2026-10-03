CREATE TABLE dns_execution_leases (
    id TEXT NOT NULL PRIMARY KEY,
    domain_id TEXT NOT NULL,
    binding_id TEXT NOT NULL,
    project_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    account_revision INTEGER NOT NULL,
    provider TEXT NOT NULL,
    credential_ref TEXT NOT NULL,
    task_id TEXT NOT NULL,
    attempt_id TEXT NOT NULL,
    attempt_number INTEGER NOT NULL,
    scheduled INTEGER NOT NULL,
    status TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (task_id, attempt_id)
);
CREATE INDEX idx_dns_leases_account ON dns_execution_leases (account_id, status);
CREATE INDEX idx_dns_leases_domain ON dns_execution_leases (domain_id, status);
