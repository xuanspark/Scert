import json, base64
import websocket
import urllib.request

resp = urllib.request.urlopen("http://127.0.0.1:9222/json/list")
pages = json.loads(resp.read().decode("utf-8"))
page = next(p for p in pages if p.get("type") == "page" and "odacm.cn" in p.get("url"))
ws_url = page["webSocketDebuggerUrl"]
print("Connecting to ws_url:", ws_url)

ws = websocket.create_connection(ws_url)

ws.send(json.dumps({
    "id": 1,
    "method": "Runtime.evaluate",
    "params": {"expression": "document.title + ' | ' + window.location.href"}
}))
res = ws.recv()
print("Page status:", res)

ws.send(json.dumps({
    "id": 2,
    "method": "Page.captureScreenshot"
}))

while True:
    msg = ws.recv()
    data = json.loads(msg)
    if data.get("id") == 2:
        img_data = base64.b64decode(data["result"]["data"])
        with open("mcp_check_screenshot.png", "wb") as f:
            f.write(img_data)
        print("Screenshot saved to mcp_check_screenshot.png")
        break

ws.close()
