package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"git.pcux.cn/xuans/Scert/backend/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

const (
	UserRoleSuperAdmin = "super_admin"
	UserRoleAdmin      = "admin"
	UserRoleUser       = "user"
	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"
)

var ErrSuperAdminProtected = errors.New("超级管理员不能删除")

// User 表示一个可登录的管理后台用户。密码摘要不会被 JSON 输出。
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type UserRepository interface {
	EnsureSuperAdmin(context.Context, User) error
	FindByIdentifier(context.Context, string) (User, error)
	ListUsers(context.Context) ([]User, error)
	GetUserByID(context.Context, string) (User, error)
	CreateUser(context.Context, User) error
	UpdateUser(context.Context, User) error
	DeleteUser(context.Context, string) error
}

type SQLUserRepository struct {
	db     *sql.DB
	driver string
}

func NewSQLUserRepository(db *sql.DB, driver ...string) *SQLUserRepository {
	selectedDriver := "sqlite"
	if len(driver) > 0 && strings.TrimSpace(driver[0]) != "" {
		selectedDriver = strings.ToLower(strings.TrimSpace(driver[0]))
	}
	return &SQLUserRepository{db: db, driver: selectedDriver}
}

func (r *SQLUserRepository) EnsureSuperAdmin(ctx context.Context, item User) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	item.Role = UserRoleSuperAdmin
	item.Status = UserStatusActive
	item = normalizeUser(item)
	if item.ID == "" {
		item.ID = "primary-super-admin"
	}
	if err := validateUser(item, false); err != nil {
		return err
	}
	if err := r.upsert(ctx, item); err != nil {
		return fmt.Errorf("同步超级管理员失败：%w", err)
	}
	return nil
}

func (r *SQLUserRepository) FindByIdentifier(ctx context.Context, identifier string) (User, error) {
	if err := r.ensureDB(); err != nil {
		return User{}, err
	}
	normalized, err := auth.NormalizeIdentifier(identifier)
	if err != nil {
		return User{}, err
	}
	row := r.db.QueryRowContext(ctx, userSelect+` WHERE LOWER(username) = ? OR (email IS NOT NULL AND LOWER(email) = ?) LIMIT 1`, normalized, normalized)
	return scanUser(row)
}

func (r *SQLUserRepository) ListUsers(ctx context.Context) ([]User, error) {
	if err := r.ensureDB(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, userSelect+` ORDER BY role, username, id`)
	if err != nil {
		return nil, fmt.Errorf("查询用户失败：%w", err)
	}
	defer rows.Close()
	items := make([]User, 0)
	for rows.Next() {
		item, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取用户失败：%w", err)
	}
	return items, nil
}

func (r *SQLUserRepository) GetUserByID(ctx context.Context, id string) (User, error) {
	if err := r.ensureDB(); err != nil {
		return User{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return User{}, errors.New("用户 ID 不能为空")
	}
	return scanUser(r.db.QueryRowContext(ctx, userSelect+` WHERE id = ?`, id))
}

func (r *SQLUserRepository) CreateUser(ctx context.Context, item User) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	item = normalizeUser(item)
	if err := validateUser(item, false); err != nil {
		return err
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO users
		(id, username, email, password_hash, role, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, item.Username, r.emailValue(item.Email), item.PasswordHash,
		item.Role, item.Status, item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return fmt.Errorf("保存用户失败：%w", err)
	}
	return nil
}

func (r *SQLUserRepository) UpdateUser(ctx context.Context, item User) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	item = normalizeUser(item)
	if err := validateUser(item, false); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE users SET username = ?, email = ?, password_hash = ?, role = ?, status = ?, updated_at = ? WHERE id = ?`,
		item.Username, r.emailValue(item.Email), item.PasswordHash, item.Role, item.Status, time.Now().UTC(), item.ID)
	if err != nil {
		return fmt.Errorf("更新用户失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取用户更新结果失败：%w", err)
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *SQLUserRepository) DeleteUser(ctx context.Context, id string) error {
	if err := r.ensureDB(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("用户 ID 不能为空")
	}
	if id == "primary-super-admin" {
		return ErrSuperAdminProtected
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("删除用户失败：%w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取用户删除结果失败：%w", err)
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *SQLUserRepository) ensureDB() error {
	if r == nil || r.db == nil {
		return errors.New("数据库连接不能为空")
	}
	return nil
}

func (r *SQLUserRepository) upsert(ctx context.Context, item User) error {
	now := time.Now().UTC()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	result, err := r.db.ExecContext(ctx, `UPDATE users SET username = ?, email = ?, password_hash = ?, role = ?, status = ?, updated_at = ? WHERE id = ?`,
		item.Username, r.emailValue(item.Email), item.PasswordHash, item.Role, item.Status, item.UpdatedAt, item.ID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO users
		(id, username, email, password_hash, role, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, item.Username, r.emailValue(item.Email), item.PasswordHash,
		item.Role, item.Status, item.CreatedAt, item.UpdatedAt)
	return err
}

func (r *SQLUserRepository) emailValue(email string) any {
	if email == "" && r.driver == "mysql" {
		return nil
	}
	return email
}

func normalizeUser(item User) User {
	item.ID = strings.TrimSpace(item.ID)
	item.Username = strings.TrimSpace(strings.ToLower(item.Username))
	item.Email = strings.TrimSpace(strings.ToLower(item.Email))
	item.Role = strings.TrimSpace(strings.ToLower(item.Role))
	item.Status = strings.TrimSpace(strings.ToLower(item.Status))
	return item
}

func validateUser(item User, allowEmptyID bool) error {
	item = normalizeUser(item)
	if item.ID == "" && !allowEmptyID {
		return errors.New("用户 ID 不能为空")
	}
	if len([]byte(item.Username)) < 3 || len([]byte(item.Username)) > 64 {
		return errors.New("用户名长度必须为 3 到 64 个字符")
	}
	if strings.Contains(item.Username, "@") {
		return errors.New("用户名格式无效")
	}
	for _, char := range item.Username {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return errors.New("用户名不能包含空格或控制字符")
		}
	}
	switch item.Username {
	case "admin", "administrator", "root", "system", "scert":
		if item.Role != UserRoleSuperAdmin || item.Username != "admin" {
			return errors.New("用户名为系统保留名称")
		}
	}
	if item.Email != "" {
		if _, err := auth.NormalizeIdentifier(item.Email); err != nil || !strings.Contains(item.Email, "@") {
			return errors.New("邮箱格式无效")
		}
	}
	if _, err := bcrypt.Cost([]byte(item.PasswordHash)); err != nil {
		return errors.New("密码摘要无效")
	}
	if item.Role != UserRoleSuperAdmin && item.Role != UserRoleAdmin && item.Role != UserRoleUser {
		return fmt.Errorf("用户角色无效：%s", item.Role)
	}
	if item.Status != UserStatusActive && item.Status != UserStatusDisabled {
		return fmt.Errorf("用户状态无效：%s", item.Status)
	}
	return nil
}

const userSelect = `SELECT id, username, email, password_hash, role, status, created_at, updated_at FROM users`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var item User
	var email sql.NullString
	if err := row.Scan(&item.ID, &item.Username, &email, &item.PasswordHash, &item.Role, &item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, sql.ErrNoRows
		}
		return User{}, fmt.Errorf("读取用户失败：%w", err)
	}
	if email.Valid {
		item.Email = email.String
	}
	return normalizeUser(item), nil
}
