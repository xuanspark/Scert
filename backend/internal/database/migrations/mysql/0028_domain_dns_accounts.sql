CREATE TABLE domain_dns_accounts (
    domain_id VARCHAR(36) NOT NULL PRIMARY KEY,
    id VARCHAR(36) NOT NULL,
    project_id VARCHAR(36) NOT NULL,
    dns_account_id VARCHAR(36) NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uq_domain_dns_accounts_id (id),
    INDEX idx_domain_dns_accounts_domain (project_id, domain_id),
    INDEX idx_domain_dns_accounts_account (project_id, dns_account_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
CREATE UNIQUE INDEX uq_dns_accounts_project_id ON dns_accounts (project_id, id);
ALTER TABLE domain_dns_accounts ADD CONSTRAINT fk_domain_dns_domain FOREIGN KEY (project_id, domain_id) REFERENCES domains (project_id, id) ON DELETE RESTRICT;
ALTER TABLE domain_dns_accounts ADD CONSTRAINT fk_domain_dns_account FOREIGN KEY (project_id, dns_account_id) REFERENCES dns_accounts (project_id, id) ON DELETE RESTRICT;
