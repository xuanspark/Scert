package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.pcux.cn/xuans/Scert/backend/internal/domain"
)

var ErrProjectReferenced = errors.New("项目仍有关联资源或成员，请先移出后重试")

// ProjectLifecycleRepository 是面向已认证业务入口的仓储，不能用基础项目仓储替代授权检查。
type ProjectLifecycleRepository interface {
	CreateManagedProject(context.Context, string, string, string) (domain.Project, error)
	UpdateManagedProject(context.Context, string, string, string, string) error
	DeleteManagedProject(context.Context, string, string) error
	RecordProjectFailure(context.Context, ProjectFailure) error
}

var _ ProjectLifecycleRepository = (*SQLProjectMemberRepository)(nil)

func (r *SQLProjectMemberRepository) CreateManagedProject(ctx context.Context, actorID, name, description string) (domain.Project, error) {
	item, err := domain.NewProject(name, description)
	if err != nil {
		return domain.Project{}, err
	}
	err = r.changeManagedProject(ctx, actorID, item, "project.create")
	if err != nil {
		return domain.Project{}, err
	}
	return item, nil
}

func (r *SQLProjectMemberRepository) UpdateManagedProject(ctx context.Context, actorID, id, name, description string) error {
	item := (domain.Project{ID: id, Name: name, Description: description}).Normalize()
	if err := item.Validate(); err != nil {
		return err
	}
	item.UpdatedAt = time.Now().UTC()
	return r.changeManagedProject(ctx, actorID, item, "project.update")
}

func (r *SQLProjectMemberRepository) DeleteManagedProject(ctx context.Context, actorID, id string) error {
	return r.changeManagedProject(ctx, actorID, domain.Project{ID: strings.TrimSpace(id)}, "project.delete")
}

// 授权在实际变更语句内完成；成功审计与变更同事务，不删除成员或业务资源来迎合删除请求。
func (r *SQLProjectMemberRepository) changeManagedProject(ctx context.Context, actorID string, item domain.Project, action string) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" || item.ID == "" {
		return ErrProjectAccessDenied
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始项目变更事务失败：%w", err)
	}
	defer tx.Rollback()
	if r.driver == "mysql" {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema=DATABASE() AND table_name IN ('projects', 'audit_logs') AND engine='InnoDB'`).Scan(&count); err != nil {
			return fmt.Errorf("检查项目审计存储失败：%w", err)
		}
		if count != 2 {
			return errors.New("项目与审计表必须使用 InnoDB，拒绝无法保证回滚的变更")
		}
	}
	if err := r.writeManagedProject(ctx, tx, actorID, item, action); err != nil {
		return err
	}
	// 不把用户输入的名称、说明或请求正文写入审计；删除后仍保留真实项目范围快照。
	result, err := tx.ExecContext(ctx, Rebind(r.driver, `INSERT INTO audit_logs
		(action, resource_type, resource_id, status, message, created_at, scope_type, project_id, task_id, operator_id)
		VALUES (?, 'project', ?, 'success', ?, ?, 'project', ?, '', ?)`),
		action, item.ID, "项目变更已完成", time.Now().UTC(), item.ID, actorID)
	if err != nil {
		return fmt.Errorf("保存项目变更审计失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return errors.New("项目变更审计未写入，已回滚本次变更")
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交项目变更失败：%w", err)
	}
	return nil
}

const projectHasReferences = `(EXISTS (SELECT 1 FROM domains d WHERE d.project_id = projects.id)
	OR EXISTS (SELECT 1 FROM servers s WHERE s.project_id = projects.id)
	OR EXISTS (SELECT 1 FROM domain_servers b WHERE b.project_id = projects.id)
	OR EXISTS (SELECT 1 FROM project_members m WHERE m.project_id = projects.id))`

func (r *SQLProjectMemberRepository) writeManagedProject(ctx context.Context, tx *sql.Tx, actorID string, item domain.Project, action string) error {
	var query string
	var args []any
	switch action {
	case "project.create":
		query = `INSERT INTO projects (id, name, description, created_at, updated_at)
			SELECT ?, ?, ?, ?, ? FROM users a WHERE a.id = ? AND a.status = 'active' AND a.role = 'super_admin'`
		args = []any{item.ID, item.Name, item.Description, item.CreatedAt, item.UpdatedAt, actorID}
	case "project.update":
		query = `UPDATE projects SET name = ?, description = ?, updated_at = ? WHERE id = ? AND ` + projectManagerPredicate
		args = []any{item.Name, item.Description, item.UpdatedAt, item.ID, actorID, item.ID}
	case "project.delete":
		query = `DELETE FROM projects WHERE id = ? AND id <> ? AND ` + projectManagerPredicate + ` AND NOT ` + projectHasReferences
		args = []any{item.ID, domain.DefaultProjectID, actorID, item.ID}
	default:
		return errors.New("项目变更动作无效")
	}
	result, err := tx.ExecContext(ctx, Rebind(r.driver, query), args...)
	if err != nil {
		return fmt.Errorf("执行项目变更失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取项目变更结果失败：%w", err)
	}
	if count == 1 {
		return nil
	}
	if count != 0 || action == "project.create" {
		return ErrProjectAccessDenied
	}
	var name, description string
	var referenced bool
	err = tx.QueryRowContext(ctx, Rebind(r.driver, `SELECT name, description, `+projectHasReferences+
		` FROM projects WHERE id = ? AND `+projectManagerPredicate), item.ID, actorID, item.ID).Scan(&name, &description, &referenced)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrProjectAccessDenied
	}
	if err != nil {
		return fmt.Errorf("核对项目变更条件失败：%w", err)
	}
	if action == "project.delete" {
		if item.ID == domain.DefaultProjectID {
			return ErrDefaultProject
		}
		if referenced {
			return ErrProjectReferenced
		}
	}
	// MySQL 秒级时间字段与相同资料可能得到零变更行；仅在当前仍有权限且内容一致时认可幂等更新。
	if action == "project.update" && r.driver == "mysql" && name == item.Name && description == item.Description {
		return nil
	}
	return errors.New("项目变更未实际生效，请刷新后重试")
}
