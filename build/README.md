# Scert 发布构建

`build-release.ps1` 默认构建 Windows amd64 和 Linux amd64 的服务端、运维辅助程序及 Web 静态资源。正式交付仅限 Web 和服务端：不调用 Wails，不打包桌面端，也不依赖桌面目录是否存在。

```text
build/release/
├── Scert-20260915-V0.1.0/
│   ├── manifest.json
│   ├── web-build/
│   ├── windows-amd64/
│   │   ├── scert.exe
│   │   ├── scert-admin.exe
│   │   ├── web/
│   │   ├── install-windows.ps1
│   │   └── SHA256SUMS
│   └── linux-amd64/
│       ├── scert
│       ├── scert-admin
│       ├── scert-nginx
│       ├── scert-storage
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

只构建 Windows 服务端和 Web：

```powershell
powershell -ExecutionPolicy Bypass -File .\build\build-release.ps1 -Target windows-amd64
```

只构建 Linux 服务端和 Web：

```powershell
powershell -ExecutionPolicy Bypass -File .\build\build-release.ps1 -Target linux-amd64
```

指定输出目录：

```powershell
powershell -ExecutionPolicy Bypass -File .\build\build-release.ps1 -OutputDirectory D:\release\Scert
```

正式构建需要 Go、Node.js 和 npm，不要求 Wails、WebView2 或 GTK 开发依赖。具体版本见 `doc/构建版本与数据库支持矩阵.md`。桌面窗口、托盘、自动更新、桌面配置及桌面 UI 测试属于范围外；`desktop/` 历史源码保留供独立开发，不得混入正式包。

旧参数 `-SkipDesktop` 保留兼容并输出警告，无论是否传入或显式设为假，都不会构建桌面端。旧参数 `-DesktopDirectory` 在构建前直接报错，避免误以为指定目录仍能加入正式发布。

脚本仅创建新的发布目录。发布目录或对应归档已存在时，在构建前拒绝执行，不覆盖旧包。前端直接构建到本次发布目录中的 `web-build/`，不清空或覆盖已有 `frontend/dist/`。失败产生的目录保留供排查，再次构建请使用新的输出目录。

`manifest.json` 记录源码提交、已跟踪文件是否未提交、实际 Go 工具链、目标平台和文件及归档的 SHA256。`delivery_scope` 固定为 `web`、`server`，兼容字段 `desktop_requested` 固定为 `false`。`source_dirty=true` 的包仅用于开发，正式发布必须从已复审提交构建。未跟踪本地产物不计入源码脏状态，也不会自动打包。

## 本机路径保护

正式服务端和辅助程序统一使用 `-trimpath`。手动构建也必须保留此参数，例如 `go build -trimpath -o scert ./cmd/scert`。前端生产构建明确关闭源码映射。

归档前逐个读取 Go 二进制构建信息，确认 `-trimpath=true`；同时扫描发布目录全部文件，拒绝工作区、用户目录、Windows 的 Go/Node 安装目录、实际 Go 工具链、模块缓存、构建缓存、临时目录和输出目录路径残留。Linux 的通用 `/usr/bin` 等运行时目录不因安装工具而被禁用。检查覆盖正反斜杠、JSON 转义、UTF-8 和 UTF-16。发现残留或无法读取构建信息时停止归档，保留失败目录用于排查。

清单仅记录 `trimpath=true` 和 `local_path_check=passed`，不会记录本机扫描路径。该检查不删除模块导入路径、相对源码文件名或程序运行时需要的部署路径，也不保证识别源码中人为写死且不属于本次构建环境的其他机器路径。

仅构建本轮 Linux 服务候选包示例：

```powershell
powershell -ExecutionPolicy Bypass -File .\build\build-release.ps1 -Target linux-amd64 -OutputDirectory .\build\release-candidate-20260927
```

此命令不构建 Windows，不代表跨平台发布验收。Linux 归档包含 `scert`、`scert-admin`、`scert-nginx`、`scert-storage`、Web 静态资源、安装器、systemd 模板、`nginx/` 策略模板和部署说明。解压后先运行 `sha256sum -c SHA256SUMS`，再按部署说明操作。`scert-nginx` 仅随包提供，普通安装器不会安装或启用它；启用前按 `nginx/Nginx辅助服务部署说明.md` 单独完成 root 策略、安全初始化、本地 socket、Nginx 接线和实机验收。

正式构建验收应检查两个平台的归档与 `manifest.json`：包含对应服务端、运维程序和 Web；不存在 `ScertDesktop`、桌面产物类型或 Wails 依赖；路径扫描及 SHA256 检查通过。不以桌面构建或 WebView2 测试作为门禁。

构建通过不等于生产验收通过；真实 DNS、ACME、SSH、Windows Service、systemd 和 HTTPS 仍需在授权环境复核。
