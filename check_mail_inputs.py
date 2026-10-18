import json, websocket, urllib.request

resp = urllib.request.urlopen("http://127.0.0.1:9222/json/list")
pages = json.loads(resp.read().decode("utf-8"))
page = next(p for p in pages if p.get("type") == "page" and "odacm.cn" in p.get("url"))
ws_url = page["webSocketDebuggerUrl"]

ws = websocket.WebSocket()
ws.connect(ws_url, timeout=5)

script = """
(() => {
    const inputs = Array.from(document.querySelectorAll('.mail-channel input, .mail-channel select'));
    return inputs.map(el => ({
        tag: el.tagName,
        type: el.type,
        placeholder: el.placeholder,
        value: el.type === 'checkbox' ? el.checked : el.value
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
