import sys

with open("doc/Scert整改任务执行清单.md", "r", encoding="utf-8") as f:
    text = f.read()

target = """## 2026年10月18日 侧边栏底部SSL证书管理字样清理与侧边栏左右5px自适应布局

- [x] 清理 DashboardNavigation.vue 底部“SSL 证书管理”角标及冗余 CSS
- [x] 优化侧边栏及账号卡片等宽自适应布局，侧边栏以卡片宽度左右各留 5px 内边距（padding: 2px 5px）
- [x] 统一第一行版本号格式为 Vx.x.x（如 V1.52.1）
- [x] 运行前端单元测试 356 项全量验证通过
- [x] 编译并成功部署前端产物至第一台服务器（8.137.23.95）
- [x] 双重推送远程仓库并持续汇报进度"""

addition = """## 2026年10月18日 新建托管域名默认勾选第一台生产测试服务器

- [x] 修改 DomainForm.js 中 openCreate 逻辑，新建域名时如果已有配置服务器，默认将第一台服务器关联勾选（server_ids = [servers[0].id]）。
- [x] 同步更新 DomainForm.test.js 对应场景断言，确保单元测试与新建默认勾选逻辑严密契合。
- [x] 前端自动化单元测试 356 项全量通过（356 pass）。
- [x] 完成前端生产环境打包构建（vite build）生成新静态资源产物。
- [ ] 部署静态产物至第一台服务器（8.137.23.95）并在 Chrome MCP 验证实机新建域名弹窗默认勾选效果。
- [ ] 提交并双重推送 Git 远端仓库。

""" + target

if target in text:
    text = text.replace(target, addition, 1)
    with open("doc/Scert整改任务执行清单.md", "w", encoding="utf-8") as f:
        f.write(text)
    print("Updated Scert整改任务执行清单.md")
else:
    print("Target not found in Scert整改任务执行清单.md")
