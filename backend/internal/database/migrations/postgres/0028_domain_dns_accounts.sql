CREATE UNIQUE INDEX uq_dns_accounts_project_id ON dns_accounts (project_id, id);
CREATE TABLE domain_dns_accounts (
    domain_id TEXT NOT NULL PRIMARY KEY,
    id TEXT NOT NULL UNIQUE,
    project_id TEXT NOT NULL,
    dns_account_id TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (project_id, domain_id) REFERENCES domains(project_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (project_id, dns_account_id) REFERENCES dns_accounts(project_id, id) ON DELETE RESTRICT
);
CREATE INDEX idx_domain_dns_accounts_account ON domain_dns_accounts (project_id, dns_account_id);
