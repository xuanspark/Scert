ALTER TABLE servers ADD COLUMN ssh_key_status TEXT NOT NULL DEFAULT 'not_configured';
ALTER TABLE servers ADD COLUMN ssh_key_fingerprint TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN ssh_key_deployed_at TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN ssh_key_tested_at TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN ssh_key_error TEXT NOT NULL DEFAULT '';
