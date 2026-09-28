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

var ErrDefaultProject = errors.New("默认项目不能删除或重复创建")

type ProjectRepository interface {
	CreateProject(context.Context, domain.Project) error
	GetProject(context.Context, string) (domain.Project, error)
	ListProjects(context.Context) ([]domain.Project, error)
	UpdateProject(context.Context, domain.Project) error
	DeleteProject(context.Context, string) error
}

type SQLProjectRepository struct {
	db     *sql.DB
	driver string
}

var _ ProjectRepository = (*SQLProjectRepository)(nil)

func NewSQLProjectRepository(db *sql.DB, driver ...string) *SQLProjectRepository {
	return &SQLProjectRepository{db: db, driver: repositoryDriver(driver)}
}

func (r *SQLProjectRepository) query(query string) string {
	return Rebind(r.driver, query)
}

func (r *SQLProjectRepository) ensureDB() error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	return nil
}

func (r *SQLProjectRepository) CreateProject(ctx context.Context, item domain.Project) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	item = item.Normalize()
	if err := item.Validate(); err != nil {
		return err
	}
	if item.ID == domain.DefaultProjectID {
		return ErrDefaultProject
	}
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, r.query(`INSERT INTO projects
		(id, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`),
		item.ID, item.Name, item.Description, now, now)
	if err != nil {
		return fmt.Errorf("创建项目失败：%w", err)
	}
	return nil
}

const projectSelect = `SELECT id, name, description, created_at, updated_at FROM projects`

func (r *SQLProjectRepository) GetProject(ctx context.Context, id string) (domain.Project, error) {
	if err := r.ensureDB(); err != nil {
		return domain.Project{}, err
	}
	return scanProject(r.db.QueryRowContext(ctx, r.query(projectSelect+` WHERE id = ?`), strings.TrimSpace(id)))
}

func (r *SQLProjectRepository) ListProjects(ctx context.Context) ([]domain.Project, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, r.query(projectSelect+`
		ORDER BY CASE WHEN id = ? THEN 0 ELSE 1 END, created_at, id`), domain.DefaultProjectID)
	if err != nil {
		return nil, fmt.Errorf("查询项目列表失败：%w", err)
	}
	defer rows.Close()
	items := make([]domain.Project, 0)
	for rows.Next() {
		item, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取项目列表失败：%w", err)
	}
	return items, nil
}

func (r *SQLProjectRepository) UpdateProject(ctx context.Context, item domain.Project) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	item = item.Normalize()
	if err := item.Validate(); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, r.query(`UPDATE projects SET name = ?, description = ?, updated_at = ? WHERE id = ?`),
		item.Name, item.Description, time.Now().UTC(), item.ID)
	if err != nil {
		return fmt.Errorf("更新项目失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取项目更新结果失败：%w", err)
	}
	if count == 0 {
		// MySQL 未实际改变字段时也可能返回零，不能将其直接解释为项目不存在。
		_, err = r.GetProject(ctx, item.ID)
		return err
	}
	return nil
}

func (r *SQLProjectRepository) DeleteProject(ctx context.Context, id string) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == domain.DefaultProjectID {
		return ErrDefaultProject
	}
	result, err := r.db.ExecContext(ctx, r.query(`DELETE FROM projects WHERE id = ?`), id)
	if err != nil {
		return fmt.Errorf("删除项目失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取项目删除结果失败：%w", err)
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func scanProject(row rowScanner) (domain.Project, error) {
	var item domain.Project
	if err := row.Scan(&item.ID, &item.Name, &item.Description, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return domain.Project{}, fmt.Errorf("读取项目失败：%w", err)
	}
	return item, nil
}
