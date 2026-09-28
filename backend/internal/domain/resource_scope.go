package domain

// ResourceScope 记录资源归属，不代替项目成员授权。空操作者表示历史数据或本地系统操作。
type ResourceScope struct {
	ProjectID string `json:"project_id"`
	OwnerID   string `json:"owner_id"`
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by"`
}

func (s ResourceScope) Normalize() ResourceScope {
	if s.ProjectID == "" {
		s.ProjectID = DefaultProjectID
	}
	return s
}
