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
│       ├── scert-nginx
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

## 本机路径保护

核心服务、辅助程序和 Wails 桌面版统一使用 `-trimpath`。手动构建也必须保留此参数，例如 `go build -trimpath -o scert ./cmd/scert` 和 `wails build -trimpath -nopackage`。前端生产构建明确关闭源码映射。

归档前逐个读取 Go 二进制构建信息，确认 `-trimpath=true`；同时扫描发布目录全部文件，拒绝工作区、用户目录、Windows 的 Go/Node 安装目录、实际 Go 工具链、模块缓存、构建缓存、临时目录和输出目录路径残留。Linux 的通用 `/usr/bin` 等运行时目录不因安装工具而被禁用。检查覆盖正反斜杠、JSON 转义、UTF-8 和 UTF-16。发现残留或无法读取构建信息时停止归档，保留失败目录用于排查。

清单仅记录 `trimpath=true` 和 `local_path_check=passed`，不会记录本机扫描路径。该检查不删除模块导入路径、相对源码文件名或程序运行时需要的部署路径，也不保证识别源码中人为写死且不属于本次构建环境的其他机器路径。

仅构建本轮 Linux 服务候选包示例：

```powershell
powershell -ExecutionPolicy Bypass -File .\build\build-release.ps1 -Target linux-amd64 -SkipDesktop -OutputDirectory .\build\release-candidate-20260915
```

此命令不构建 Windows 或 Wails 桌面端，不代表跨平台发布验收。归档包含 `scert`、`scert-admin`、`scert-nginx`、Web 静态资源、安装器、systemd 模板和部署说明。解压后先运行 `sha256sum -c SHA256SUMS`，再按部署说明操作。`scert-nginx` 仅随包提供，普通安装器不会安装或启用它；启用前仍需单独完成 root 策略、本地 socket、Nginx 接线和实机验收。

构建通过不等于生产验收通过；真实 DNS、ACME、SSH、Windows Service、systemd、WebView2 和 HTTPS 仍需在授权环境复核。
