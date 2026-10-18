import json, websocket, urllib.request

resp = urllib.request.urlopen("http://127.0.0.1:9222/json/list")
pages = json.loads(resp.read().decode("utf-8"))
page = next(p for p in pages if p.get("type") == "page" and "odacm.cn" in p.get("url"))
ws_url = page["webSocketDebuggerUrl"]

ws = websocket.WebSocket()
ws.connect(ws_url, timeout=5)

# 查找所有 nav item
script = """
(() => {
    const navItems = Array.from(document.querySelectorAll('.dashboard-navigation-link, .nav-item, nav a, nav button, aside button, aside a'));
    return navItems.map(el => ({
        tag: el.tagName,
        text: el.innerText.trim(),
        href: el.getAttribute('href'),
        classes: el.className
    }));
})()
"""
ws.send(json.dumps({
    "id": 1,
    "method": "Runtime.evaluate",
    "params": {"expression": script, "returnByValue": True}
}))
print(ws.recv())
ws.close()
