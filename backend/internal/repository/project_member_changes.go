package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

// 成员写入仍在单条语句内检查权限；审计与变更共用事务，任何一步失败都不留下成功变更。
func (r *SQLProjectMemberRepository) changeProjectRole(ctx context.Context, actorID, projectID, userID string, role domain.ProjectRole, grant bool) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	actorID, projectID, userID = strings.TrimSpace(actorID), strings.TrimSpace(projectID), strings.TrimSpace(userID)
	if actorID == "" || projectID == "" || userID == "" || !role.Valid() {
		return ErrProjectAccessDenied
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始项目成员变更事务失败：%w", err)
	}
	defer tx.Rollback()
	if r.driver == "mysql" {
		// 历史库可能使用非事务引擎，不能仅凭 BeginTx 成功就承诺审计和权限一起回滚。
		var transactionalTables int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema=DATABASE() AND table_name IN ('project_members', 'audit_logs')
			AND engine='InnoDB'`).Scan(&transactionalTables); err != nil {
			return fmt.Errorf("检查项目成员审计存储失败：%w", err)
		}
		if transactionalTables != 2 {
			return fmt.Errorf("项目成员和审计表必须使用 InnoDB，拒绝无法保证回滚的权限变更")
		}
	}
	now := time.Now().UTC()
	if err := r.writeProjectRole(ctx, tx, actorID, projectID, userID, role, grant, now); err != nil {
		return err
	}
	action, message := "project_member.revoke", "撤销项目角色："
	if grant {
		action, message = "project_member.grant", "授予项目角色："
	}
	// 范围来自刚通过授权条件的实际成员变更，不在自我撤权后重新推测角色或审计范围。
	// 不调用使用独立连接的审计方法，以免脱离事务或阻塞单连接 SQLite。
	result, err := tx.ExecContext(ctx, Rebind(r.driver, `INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at, scope_type, project_id, task_id, operator_id)
		VALUES (?, 'project_member', ?, 'success', ?, ?, ?, ?, '', ?)`),
		action, userID, message+string(role), now, ScopeProject, projectID, actorID)
	if err != nil {
		return fmt.Errorf("保存项目成员变更审计失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取项目成员审计写入结果失败：%w", err)
	}
	if count != 1 {
		return fmt.Errorf("项目成员审计未写入，已拒绝本次权限变更")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交项目成员变更失败：%w", err)
	}
	return nil
}

func (r *SQLProjectMemberRepository) writeProjectRole(ctx context.Context, tx *sql.Tx, actorID, projectID, userID string, role domain.ProjectRole, grant bool, now time.Time) error {
	query := `DELETE FROM project_members WHERE project_id = ? AND user_id = ? AND role = ? AND ` + projectManagerPredicate
	args := []any{projectID, userID, role, actorID, projectID}
	if grant {
		query = `INSERT INTO project_members (project_id, user_id, role, created_at)
			SELECT p.id, u.id, ?, ? FROM projects p JOIN users u ON u.id = ? AND u.status = 'active'
			WHERE p.id = ? AND ` + projectManagerPredicate
		args = []any{role, now, userID, projectID, actorID, projectID}
	} else if r.driver == "mysql" {
		// MySQL 撤销使用自连接，避免不兼容的同表删除子查询。
		query = `DELETE target FROM project_members target
			JOIN users a ON a.id = ? AND a.status = 'active'
			LEFT JOIN project_members manager ON manager.project_id = target.project_id
			AND manager.user_id = a.id AND manager.role = 'project_admin'
			WHERE target.project_id = ? AND target.user_id = ? AND target.role = ?
			AND (a.role = 'super_admin' OR (a.role IN ('admin', 'user') AND manager.user_id IS NOT NULL))`
		args = []any{actorID, projectID, userID, role}
	}
	result, err := tx.ExecContext(ctx, Rebind(r.driver, query), args...)
	if err != nil {
		return fmt.Errorf("变更项目角色失败：%w", err)
	}
	return projectMemberWriteResult(result)
}
