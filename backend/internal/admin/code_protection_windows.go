//go:build windows

package admin

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

func protectCodeFile(path string) error {
	currentSID, err := currentProcessUserSID()
	if err != nil {
		return err
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("创建系统账户安全标识失败：%w", err)
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("创建本地管理员组安全标识失败：%w", err)
	}

	var pinner runtime.Pinner
	pinner.Pin(currentSID)
	pinner.Pin(systemSID)
	pinner.Pin(adminSID)
	defer pinner.Unpin()

	readDelete := windows.ACCESS_MASK(windows.FILE_GENERIC_READ | windows.DELETE | windows.READ_CONTROL | windows.SYNCHRONIZE)
	fullControl := windows.ACCESS_MASK(windows.FILE_GENERIC_READ |
		windows.FILE_GENERIC_WRITE |
		windows.FILE_GENERIC_EXECUTE |
		windows.DELETE |
		windows.READ_CONTROL |
		windows.WRITE_DAC |
		windows.WRITE_OWNER |
		windows.SYNCHRONIZE)
	entries := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: readDelete,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(currentSID),
			},
		},
		{
			AccessPermissions: fullControl,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(systemSID),
			},
		},
		{
			AccessPermissions: fullControl,
			AccessMode:        windows.SET_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_ALIAS,
				TrusteeValue: windows.TrusteeValueFromSID(adminSID),
			},
		},
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("创建校验码文件访问控制列表失败：%w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("应用校验码文件访问控制列表失败：%w", err)
	}
	return nil
}

func currentProcessUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("读取当前服务账户安全标识失败：%w", err)
	}
	if user == nil || user.User.Sid == nil {
		return nil, fmt.Errorf("当前服务账户没有有效的安全标识")
	}
	return user.User.Sid, nil
}
