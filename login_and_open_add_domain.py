import json, base64, time
import websocket, urllib.request

resp = urllib.request.urlopen("http://127.0.0.1:9222/json/list")
pages = json.loads(resp.read().decode("utf-8"))
page = next(p for p in pages if p.get("type") == "page" and "odacm.cn" in p.get("url"))
ws_url = page["webSocketDebuggerUrl"]

ws = websocket.WebSocket()
ws.connect(ws_url, timeout=5)

# 自动填写登录表单并登录 xuans / 10102442Lb! (备用 12345678a)
login_script = """
(() => {
    const inputs = Array.from(document.querySelectorAll('input'));
    const userInput = inputs.find(i => i.placeholder && i.placeholder.includes('用户名') || i.type === 'text');
    const pwdInput = inputs.find(i => i.type === 'password');
    const submitBtn = document.querySelector('button[type="submit"]') || Array.from(document.querySelectorAll('button')).find(b => b.innerText.includes('登录'));
    
    if (userInput && pwdInput && submitBtn) {
        userInput.value = 'xuans';
        userInput.dispatchEvent(new Event('input', { bubbles: true }));
        pwdInput.value = '10102442Lb!';
        pwdInput.dispatchEvent(new Event('input', { bubbles: true }));
        submitBtn.click();
        return 'Submitting login...';
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

# 查看是否进入控制台并点击“域名配置”
nav_script = """
(() => {
    const items = Array.from(document.querySelectorAll('.navigation-item, button'));
    const target = items.find(b => b.innerText.includes('域名配置'));
    if (target) {
        target.click();
        return 'Navigated to 域名配置';
    }
    return 'Not in dashboard yet: ' + document.body.innerText.slice(0, 100);
})()
"""
ws.send(json.dumps({
    "id": 2,
    "method": "Runtime.evaluate",
    "params": {"expression": nav_script, "returnByValue": True}
}))
print(ws.recv())

time.sleep(1)

# 点击“添加域名”
add_script = """
(() => {
    const btn = Array.from(document.querySelectorAll('button')).find(b => b.innerText.includes('添加域名'));
    if (btn) {
        btn.click();
        return 'Clicked 添加域名';
    }
    return '添加域名 not found';
})()
"""
ws.send(json.dumps({
    "id": 3,
    "method": "Runtime.evaluate",
    "params": {"expression": add_script, "returnByValue": True}
}))
print(ws.recv())

time.sleep(1)

# 截图保存
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
