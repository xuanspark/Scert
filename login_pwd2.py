import json, base64, time
import websocket, urllib.request

resp = urllib.request.urlopen("http://127.0.0.1:9222/json/list")
pages = json.loads(resp.read().decode("utf-8"))
page = next(p for p in pages if p.get("type") == "page" and "odacm.cn" in p.get("url"))
ws_url = page["webSocketDebuggerUrl"]

ws = websocket.WebSocket()
ws.connect(ws_url, timeout=5)

# 使用 12345678a 尝试登录
login_script = """
(() => {
    const inputs = Array.from(document.querySelectorAll('input'));
    const userInput = inputs.find(i => i.placeholder && i.placeholder.includes('用户名') || i.type === 'text');
    const pwdInput = inputs.find(i => i.type === 'password');
    const submitBtn = document.querySelector('button[type="submit"]') || Array.from(document.querySelectorAll('button')).find(b => b.innerText.includes('登录'));
    
    if (userInput && pwdInput && submitBtn) {
        userInput.value = 'xuans';
        userInput.dispatchEvent(new Event('input', { bubbles: true }));
        pwdInput.value = '12345678a';
        pwdInput.dispatchEvent(new Event('input', { bubbles: true }));
        submitBtn.click();
        return 'Submitting login with 12345678a...';
    }
    return 'Login elements not found';
})()
"""

ws.send(json.dumps({
    "id": 1,
    "method": "Runtime.evaluate",
    "params": {"expression": login_script, "returnByValue": True}
}))
print(ws.recv())

time.sleep(2)

ws.send(json.dumps({
    "id": 2,
    "method": "Runtime.evaluate",
    "params": {"expression": "document.body.innerText.slice(0, 150)", "returnByValue": True}
}))
print(ws.recv())

ws.close()
