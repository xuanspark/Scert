with open("doc/Scert整改任务执行清单.md", "r", encoding="utf-8") as f:
    text = f.read()

text = text.replace(
    "- [ ] 部署静态产物至第一台服务器（8.137.23.95）并在 Chrome MCP 验证实机新建域名弹窗默认勾选效果。\n- [ ] 提交并双重推送 Git 远端仓库。",
    "- [✓] 部署静态产物至第一台服务器（8.137.23.95）并在 Chrome MCP 验证实机新建域名弹窗默认勾选效果。\n- [✓] 提交并双重推送 Git 远端仓库。"
)
with open("doc/Scert整改任务执行清单.md", "w", encoding="utf-8") as f:
    f.write(text)

with open("doc/Scert整改合并优化清单.md", "r", encoding="utf-8") as f:
    text_m = f.read()

text_m = text_m.replace(
    "- [ ] 部署静态产物至第一台服务器（8.137.23.95）并在 Chrome MCP 验证实机新建域名弹窗默认勾选效果。\n- [ ] 提交并双重推送 Git 远端仓库。",
    "- [✓] 部署静态产物至第一台服务器（8.137.23.95）并在 Chrome MCP 验证实机新建域名弹窗默认勾选效果。\n- [✓] 提交并双重推送 Git 远端仓库。"
)
with open("doc/Scert整改合并优化清单.md", "w", encoding="utf-8") as f:
    f.write(text_m)

print("Marked status as completed in execution lists.")
