# 跨模块测试目录

本目录供后续正式跨模块和端到端测试使用，目前不代表已经建立独立测试套件。测试代码可纳入 Git，运行结果、截图、缓存和数据库不可纳入 Git。

现有正式测试保留原位，不为目录整理而删除或迁移：

- 后端：`backend/internal/` 下的 `*_test.go`，在 `backend/` 执行 `go test ./...` 和 `go vet ./...`。
- MySQL 隔离实库：`backend/internal/database/mysql_integration_test.go`；显式提供程序和产物目录，分别验证 5.7/8.0，参数及安全边界见《构建版本与数据库支持矩阵.md》。默认跳过不算实库通过。
- 前端：页面旁的 `*.test.js`，在 `frontend/` 执行 `npm.cmd test`。
- 安装器：`deploy/tests/install-linux-test.py` 是已有正式安装器测试，不属于临时辅助脚本。
- 产物边界：`backend/internal/releasearchive/source_boundary_test.go`，在隔离 Git 仓库验证实际忽略行为，不依赖开发机 `.git/info/exclude`。

生产验收仍须使用对应候选包在真实环境运行。测试夹具不能代替真实 DNS、ACME、SSH、HTTPS、服务管理或恢复演练。
