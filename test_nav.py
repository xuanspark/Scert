import json, websocket, urllib.request

resp = urllib.request.urlopen("http://127.0.0.1:9222/json/list")
pages = json.loads(resp.read().decode("utf-8"))
page = next(p for p in pages if p.get("type") == "page" and "odacm.cn" in p.get("url"))
ws_url = page["webSocketDebuggerUrl"]

ws = websocket.WebSocket()
ws.connect(ws_url, timeout=5)

# 点击侧边栏的“系统设置”或切换视图查看
script = """
(() => {
    const navItems = Array.from(document.querySelectorAll('.dashboard-navigation-item, .nav-item, button'));
    return navItems.map(item => item.innerText.trim()).filter(Boolean);
})()
"""
ws.send(json.dumps({
    "id": 1,
    "method": "Runtime.evaluate",
    "params": {"expression": script, "returnByValue": True}
}))

res = ws.recv()
print("Nav elements:", res)
ws.close()
