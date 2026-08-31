ALTER TABLE servers ADD COLUMN validation_command_json TEXT NULL;
UPDATE servers SET validation_command_json = '[]' WHERE validation_command_json IS NULL OR validation_command_json = '';
ALTER TABLE servers MODIFY COLUMN validation_command_json TEXT NOT NULL;
