# Scert 发布构建

`build-release.ps1` 默认构建 Windows amd64 和 Linux amd64 的核心服务与密码重置程序。当前主机能够直接构建的 Wails 桌面版也会同时写入对应平台目录，并记录到 `manifest.json`。

```text
build/release/
├── manifest.json
├── windows-amd64/
│   ├── scert.exe
│   ├── scert-admin.exe
│   └── ScertDesktop.exe
└── linux-amd64/
    ├── scert
    └── scert-admin
```

## 使用方法

在项目根目录执行：

```powershell
powershell -ExecutionPolicy Bypass -File .\build\build-release.ps1
```

只构建 Windows 核心服务和桌面版：

```powershell
powershell -ExecutionPolicy Bypass -File .\build\build-release.ps1 -Target windows-amd64
```

只构建核心服务，不构建桌面版：

```powershell
powershell -ExecutionPolicy Bypass -File .\build\build-release.ps1 -SkipDesktop
```

指定输出目录：

```powershell
powershell -ExecutionPolicy Bypass -File .\build\build-release.ps1 -OutputDirectory D:\release\Scert
```

桌面版构建需要已安装 Wails v2、Go 和 Node.js。Wails 桌面版使用当前主机原生平台构建；Windows 主机不能直接完成 Linux GTK 桌面交叉构建，Linux 桌面产物需要在 Linux 主机上执行同一脚本。

脚本不会删除输出目录中的其他文件，只会创建目录并覆盖本次目标平台对应的程序文件和校验清单。

构建通过不等于生产验收通过；真实 DNS、ACME、SSH、Windows Service、systemd、WebView2 和 HTTPS 仍需在授权环境复核。
