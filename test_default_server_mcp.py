import json, base64, time
import websocket, urllib.request

resp = urllib.request.urlopen("http://127.0.0.1:9222/json/list")
pages = json.loads(resp.read().decode("utf-8"))
page = next(p for p in pages if p.get("type") == "page" and "odacm.cn" in p.get("url"))
ws_url = page["webSocketDebuggerUrl"]

ws = websocket.WebSocket()
ws.connect(ws_url, timeout=5)

# 清除缓存并刷新页面
ws.send(json.dumps({
    "id": 1,
    "method": "Network.clearBrowserCache"
}))
print(ws.recv())

ws.send(json.dumps({
    "id": 2,
    "method": "Page.reload",
    "params": {"ignoreCache": True}
}))
print(ws.recv())

time.sleep(2)

# 点击“添加域名”
script = """
(() => {
    const btn = Array.from(document.querySelectorAll('button')).find(b => b.innerText.includes('添加域名'));
    if (btn) {
        btn.click();
        return 'Clicked 添加域名';
    }
    return 'Not found';
})()
"""
ws.send(json.dumps({
    "id": 3,
    "method": "Runtime.evaluate",
    "params": {"expression": script, "returnByValue": True}
}))
print(ws.recv())

time.sleep(1)

ws.send(json.dumps({
    "id": 4,
    "method": "Page.captureScreenshot"
}))
msg = ws.recv()
data = json.loads(msg)
if "result" in data and "data" in data["result"]:
    with open("mcp_add_domain_default_checked.png", "wb") as f:
        f.write(base64.b64decode(data["result"]["data"]))
    print("Screenshot saved to mcp_add_domain_default_checked.png")

ws.close()
