export async function requestProjectAPI(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    credentials: "include",
    cache: "no-store",
    headers: {
      ...(options.body ? { "Content-Type": "application/json" } : {}),
      ...(options.headers || {}),
    },
  });
  const result = response.status === 204 ? {} : await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(result.error || "项目请求失败，请稍后重试");
    error.status = response.status;
    error.requestID = /^[a-zA-Z0-9_-]{1,128}$/.test(result.request_id || "") ? result.request_id : "";
    throw error;
  }
  return result;
}

export function handleProjectSession(error) {
  if (error.status === 401) {
    window.location.assign("/login");
    return "登录会话已过期，请重新登录";
  }
  if (!error.status) return "项目请求失败，请检查网络后重试";
  const message = error.message || "项目请求失败，请稍后重试";
  return error.requestID ? `${message}（请求编号：${error.requestID}）` : message;
}

export const pageSizes = [5, 10, 15, 30];
