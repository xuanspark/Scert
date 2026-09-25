ALTER TABLE servers ADD COLUMN auth_method TEXT NOT NULL DEFAULT 'private_key';
ALTER TABLE servers ADD COLUMN password_ref TEXT NOT NULL DEFAULT '';
