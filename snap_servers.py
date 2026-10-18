import json, base64, time
import websocket, urllib.request

resp = urllib.request.urlopen("http://127.0.0.1:9222/json/list")
pages = json.loads(resp.read().decode("utf-8"))
page = next(p for p in pages if p.get("type") == "page" and "odacm.cn" in p.get("url"))
ws_url = page["webSocketDebuggerUrl"]

ws = websocket.WebSocket()
ws.connect(ws_url, timeout=5)

time.sleep(1)

ws.send(json.dumps({
    "id": 1,
    "method": "Page.captureScreenshot"
}))
msg = ws.recv()
data = json.loads(msg)
if "result" in data and "data" in data["result"]:
    with open("mcp_servers_view.png", "wb") as f:
        f.write(base64.b64decode(data["result"]["data"]))
    print("Screenshot saved to mcp_servers_view.png")

ws.close()
