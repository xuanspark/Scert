import json, base64, time
import websocket
import urllib.request

resp = urllib.request.urlopen("http://127.0.0.1:9222/json/list")
pages = json.loads(resp.read().decode("utf-8"))
page = next(p for p in pages if p.get("type") == "page" and "odacm.cn" in p.get("url"))
ws_url = page["webSocketDebuggerUrl"]
print("Connecting to ws_url:", ws_url)

ws = websocket.WebSocket()
ws.connect(ws_url, timeout=5)

ws.send(json.dumps({
    "id": 1,
    "method": "Runtime.evaluate",
    "params": {"expression": "document.title + ' | ' + window.location.href"}
}))

msg = ws.recv()
print("Eval result:", msg)

ws.send(json.dumps({
    "id": 2,
    "method": "Page.captureScreenshot"
}))

msg2 = ws.recv()
data = json.loads(msg2)
if "result" in data and "data" in data["result"]:
    with open("mcp_verified.png", "wb") as f:
        f.write(base64.b64decode(data["result"]["data"]))
    print("Screenshot captured successfully!")
else:
    print("Response:", msg2[:200])

ws.close()
