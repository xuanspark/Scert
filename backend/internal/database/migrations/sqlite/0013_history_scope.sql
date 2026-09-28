ALTER TABLE tasks ADD COLUMN scope_type TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE tasks ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_logs ADD COLUMN scope_type TEXT NOT NULL DEFAULT 'unknown';
ALTER TABLE audit_logs ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_logs ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_logs ADD COLUMN operator_id TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_tasks_project_history ON tasks(project_id, created_at);
CREATE INDEX idx_audit_project_history ON audit_logs(project_id, created_at);
CREATE INDEX idx_audit_task_history ON audit_logs(task_id);
