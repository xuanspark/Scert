import sys

with open("doc/Scert整改合并优化清单.md", "r", encoding="utf-8") as f:
    text = f.read()

target = """## 2026年10月17日 邮件模板美化与Linux生产主服务热部署"""

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
    with open("doc/Scert整改合并优化清单.md", "w", encoding="utf-8") as f:
        f.write(text)
    print("Updated Scert整改合并优化清单.md")
else:
    print("Target not found in Scert整改合并优化清单.md")
