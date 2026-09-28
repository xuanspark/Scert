package domain

import "testing"

func TestProjectRolePermissionMatrix(t *testing.T) {
	permissions := []ProjectPermission{PermissionProjectRead, PermissionProjectManage, PermissionMembersManage,
		PermissionCertificateRead, PermissionCertificateManage, PermissionPrivateKeyRead, PermissionServerRead,
		PermissionServerManage, PermissionDeploymentRead, PermissionDeploymentExecute, PermissionDeploymentRollback,
		PermissionDNSRead, PermissionDNSManage, PermissionDNSCredentialUse, PermissionAuditRead, PermissionTaskRead,
		PermissionTaskCancel, "", "system.manage", "certificate.private_key.export"}
	cases := []struct {
		role    ProjectRole
		allowed []ProjectPermission
	}{
		{ProjectRoleAdmin, permissions[:17]},
		{ProjectRoleCertificateAdmin, []ProjectPermission{PermissionProjectRead, PermissionCertificateRead, PermissionCertificateManage, PermissionPrivateKeyRead}},
		{ProjectRoleDeploymentAdmin, []ProjectPermission{PermissionProjectRead, PermissionCertificateRead, PermissionServerRead, PermissionServerManage, PermissionDeploymentRead, PermissionDeploymentExecute, PermissionDeploymentRollback}},
		{ProjectRoleDNSAdmin, []ProjectPermission{PermissionProjectRead, PermissionDNSRead, PermissionDNSManage, PermissionDNSCredentialUse}},
		{ProjectRoleAuditViewer, []ProjectPermission{PermissionProjectRead, PermissionAuditRead, PermissionTaskRead}},
		{ProjectRoleReadOnly, []ProjectPermission{PermissionProjectRead, PermissionCertificateRead, PermissionServerRead, PermissionDeploymentRead, PermissionDNSRead, PermissionTaskRead}},
		{"", nil}, {"super_admin", nil}, {"PROJECT_ADMIN", nil},
	}
	for _, tc := range cases {
		t.Run(string(tc.role), func(t *testing.T) {
			for _, p := range permissions {
				want := false
				for _, granted := range tc.allowed {
					want = want || granted == p
				}
				if got := tc.role.Allows(p); got != want {
					t.Errorf("角色 %q 操作 %q 判定为 %t，期望 %t", tc.role, p, got, want)
				}
			}
		})
	}
}
