ALTER TABLE tasks ADD COLUMN scope_type VARCHAR(16) NOT NULL DEFAULT 'unknown';
ALTER TABLE tasks ADD COLUMN project_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '';
ALTER TABLE audit_logs ADD COLUMN scope_type VARCHAR(16) NOT NULL DEFAULT 'unknown';
ALTER TABLE audit_logs ADD COLUMN project_id VARCHAR(36) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '';
ALTER TABLE audit_logs ADD COLUMN task_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE audit_logs ADD COLUMN operator_id VARCHAR(36) NOT NULL DEFAULT '';
CREATE INDEX idx_tasks_project_history ON tasks(project_id, created_at);
CREATE INDEX idx_audit_project_history ON audit_logs(project_id, created_at);
CREATE INDEX idx_audit_task_history ON audit_logs(task_id);
