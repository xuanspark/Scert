package domain

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const DefaultProjectID = "default"

var projectIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,35}$`)

type Project struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func NewProject(name, description string) (Project, error) {
	now := time.Now().UTC()
	item := (Project{
		ID: NewID(), Name: name, Description: description,
		CreatedAt: now, UpdatedAt: now,
	}).Normalize()
	if err := item.Validate(); err != nil {
		return Project{}, err
	}
	return item, nil
}

func (p Project) Normalize() Project {
	p.ID = strings.TrimSpace(p.ID)
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	return p
}

func (p Project) Validate() error {
	if !projectIDPattern.MatchString(p.ID) {
		return errors.New("项目 ID 必须为 1～36 位小写字母、数字、连字符或下划线，并以字母或数字开头")
	}
	if !utf8.ValidString(p.Name) || strings.TrimSpace(p.Name) == "" || utf8.RuneCountInString(p.Name) > 128 {
		return errors.New("项目名称必须为 1～128 个字符")
	}
	if strings.ContainsFunc(p.Name, unicode.IsControl) {
		return errors.New("项目名称不能包含控制字符")
	}
	if !utf8.ValidString(p.Description) || utf8.RuneCountInString(p.Description) > 4096 {
		return errors.New("项目说明不能超过 4096 个字符")
	}
	if strings.ContainsFunc(p.Description, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
	}) {
		return errors.New("项目说明包含无效控制字符")
	}
	return nil
}
