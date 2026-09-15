# Scert 发布构建

`build-release.ps1` 默认构建 Windows amd64 和 Linux amd64 的核心服务与密码重置程序。当前主机能够直接构建的 Wails 桌面版也会同时写入对应平台目录，并记录到 `manifest.json`。

```text
build/release/
├── Scert-20260915-V0.1.0/
│   ├── manifest.json
│   ├── web-build/
│   ├── windows-amd64/
│   │   ├── scert.exe
│   │   ├── scert-admin.exe
│   │   └── ScertDesktop.exe
│   └── linux-amd64/
│       ├── scert
│       ├── scert-admin
│       ├── web/
│       └── SHA256SUMS
├── Scert-20260915-V0.1.0-linux-amd64.tar.gz
└── Scert-20260915-V0.1.0-windows-amd64.zip
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

脚本仅创建新的发布目录。发布目录或对应归档已存在时，在构建前拒绝执行，不覆盖旧包。前端直接构建到本次发布目录中的 `web-build/`，不清空或覆盖已有 `frontend/dist/`。失败产生的目录保留供排查，再次构建请使用新的输出目录。

`manifest.json` 记录源码提交、已跟踪文件是否未提交、实际 Go 工具链、目标平台、是否请求桌面端以及文件和归档的 SHA256。`source_dirty=true` 的包仅用于开发，正式发布必须从已复审提交构建。未跟踪本地产物不计入源码脏状态，也不会自动打包。

仅构建本轮 Linux 服务候选包示例：

```powershell
powershell -ExecutionPolicy Bypass -File .\build\build-release.ps1 -Target linux-amd64 -SkipDesktop -OutputDirectory .\build\release-candidate-20260915
```

此命令不构建 Windows 或 Wails 桌面端，不代表跨平台发布验收。归档包含 `scert`、`scert-admin`、Web 静态资源、安装器、systemd 模板和部署说明。解压后先运行 `sha256sum -c SHA256SUMS`，再按部署说明操作。

构建通过不等于生产验收通过；真实 DNS、ACME、SSH、Windows Service、systemd、WebView2 和 HTTPS 仍需在授权环境复核。
