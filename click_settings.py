import json, base64, time
import websocket, urllib.request

resp = urllib.request.urlopen("http://127.0.0.1:9222/json/list")
pages = json.loads(resp.read().decode("utf-8"))
page = next(p for p in pages if p.get("type") == "page" and "odacm.cn" in p.get("url"))
ws_url = page["webSocketDebuggerUrl"]

ws = websocket.WebSocket()
ws.connect(ws_url, timeout=5)

script = """
(() => {
    const all = Array.from(document.querySelectorAll('span, button, a, div'));
    const target = all.find(el => el.innerText && el.innerText.trim() === '系统设置');
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

time.sleep(1)

ws.send(json.dumps({
    "id": 2,
    "method": "Page.captureScreenshot"
}))
msg = ws.recv()
data = json.loads(msg)
if "result" in data and "data" in data["result"]:
    with open("mcp_settings_view.png", "wb") as f:
        f.write(base64.b64decode(data["result"]["data"]))
    print("Screenshot saved to mcp_settings_view.png")

ws.close()
