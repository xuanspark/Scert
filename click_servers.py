import json, websocket, urllib.request

resp = urllib.request.urlopen("http://127.0.0.1:9222/json/list")
pages = json.loads(resp.read().decode("utf-8"))
page = next(p for p in pages if p.get("type") == "page" and "odacm.cn" in p.get("url"))
ws_url = page["webSocketDebuggerUrl"]

ws = websocket.WebSocket()
ws.connect(ws_url, timeout=5)

# 点击“部署服务器”
script = """
(() => {
    const all = Array.from(document.querySelectorAll('span, button, a, div'));
    const target = all.find(el => el.innerText && el.innerText.trim() === '部署服务器');
    if (target) {
        target.click();
        return 'Clicked ' + target.tagName;
    }
    return 'Not found';
})()
"""
ws.send(json.dumps({
    "id": 1,
    "method": "Runtime.evaluate",
    "params": {"expression": script, "returnByValue": True}
}))
print(ws.recv())
ws.close()
