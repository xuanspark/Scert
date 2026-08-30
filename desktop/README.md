# Scert 桌面版

桌面版使用 Wails 2，将 Vue 管理页面嵌入桌面窗口，并在同一进程内复用 Scert Go 核心服务和 HTTP API。桌面版不会额外监听服务端口，也不会复制一套桌面专用业务页面。

## 开发运行

在项目根目录先构建前端，再进入桌面目录执行：

```powershell
npm.cmd --prefix .\frontend run build
wails dev
```

首次构建或发布构建会通过 `desktop/scripts/build-frontend.mjs` 构建根目录前端，并同步到桌面嵌入目录。

## 配置目录

默认使用当前用户配置目录下的 `Scert` 目录。可以通过以下环境变量覆盖：

- `SCERT_DESKTOP_BASE_DIR`：桌面版运行数据基础目录。
- `SCERT_CONFIG_PATH`：明确指定 `scert.json` 配置文件路径。

桌面版和 Web 版使用相同的配置格式、数据库、证书目录、DNS API 和部署逻辑。
