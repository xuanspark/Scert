ALTER TABLE servers ADD COLUMN ssh_key_status VARCHAR(32) NOT NULL DEFAULT 'not_configured';
ALTER TABLE servers ADD COLUMN ssh_key_fingerprint VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN ssh_key_deployed_at VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN ssh_key_tested_at VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN ssh_key_error TEXT NOT NULL;
