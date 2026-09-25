package repository

import (
	"reflect"
	"strings"
	"testing"
)

func TestUserGroupsValidationAndPersistence(t *testing.T) {
	db := openUserTestDatabase(t)
	defer db.Close()
	repo := NewSQLUserRepository(db.DB)
	item := User{ID: "group-admin", Username: "group-admin", Role: UserRoleAdmin, Status: UserStatusActive, PasswordHash: userTestPasswordHash(t, "Member123a"), Groups: []string{" beta ", "alpha", "alpha"}}
	if err := repo.CreateUser(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetUserByID(t.Context(), item.ID)
	if err != nil || !reflect.DeepEqual(got.Groups, []string{"alpha", "beta"}) {
		t.Fatalf("分组未规范化保存：%+v %v", got.Groups, err)
	}
	got.Role = UserRoleUser
	if err := repo.UpdateUser(t.Context(), got); err == nil {
		t.Fatal("仓储层应拒绝普通用户多组")
	}
	got.Groups = []string{"alpha"}
	if err := repo.UpdateUser(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB.Exec("UPDATE users SET groups_json = NULL WHERE id = ?", item.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := repo.GetUserByID(t.Context(), item.ID)
	if err != nil || len(legacy.Groups) != 0 {
		t.Fatal("旧用户应归入默认分组")
	}
	for _, groups := range [][]string{{""}, {"默认分组"}, {"bad\nname"}, {strings.Repeat("组", 33)}, make([]string, 33)} {
		if _, err := NormalizeUserGroups(UserRoleAdmin, groups); err == nil {
			t.Fatalf("非法分组未拒绝：%v", groups)
		}
	}
}
