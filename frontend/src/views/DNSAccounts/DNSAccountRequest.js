export async function requestDNS(path, options = {}) {
  let response;
  try {
    response = await fetch(path, {
      ...options,
      credentials: "include",
      cache: "no-store",
      headers: options.body ? { "Content-Type": "application/json" } : {},
    });
  } catch (reason) {
    if (reason.name === "AbortError") throw reason;
    throw new Error("网络连接失败；操作结果可能尚未确认，请刷新列表后核对，勿重复提交");
  }
  if (!response.ok) {
    // 不展示任意服务端正文，避免代理错误或服务商响应带出敏感内容。
    const messages = {
      400: "参数无效，请检查账户名称、凭据和测试域名",
      401: "登录会话已过期，请重新登录",
      403: "当前账号没有操作权限，或目标账户已不可用",
      409: "账户已变更，请刷新后重新选择，不会自动重试此次操作",
      422: "DNS 在线验证失败，请检查凭据、测试域名和云端权限",
      429: "操作过于频繁，请稍后重试",
    };
    const error = new Error(messages[response.status] || "DNS 账户请求失败，请稍后刷新核对");
    error.status = response.status;
    throw error;
  }
  try {
    return await response.json();
  } catch {
    throw new Error("服务器响应格式不正确，请刷新核对操作结果");
  }
}

export async function loadDNSProjects(signal) {
  const projects = [];
  const cursors = new Set();
  let after = "";
  while (true) {
    const query = new URLSearchParams({ page_size: "30", after });
    const result = await requestDNS(`/api/v1/projects?${query}`, { signal });
    if (!Array.isArray(result.items)) throw new Error("项目列表格式不正确，请刷新重试");
    projects.push(...result.items);
    if (!result.has_more) return projects;
    if (typeof result.next_cursor !== "string" || !result.next_cursor || cursors.has(result.next_cursor)) {
      throw new Error("项目分页游标无效，请刷新重试");
    }
    after = result.next_cursor;
    cursors.add(after);
  }
}
