import { onBeforeUnmount, onMounted, ref } from "vue";

export function useAppShell() {
  const setupState = ref(null);
  const authenticated = ref(null);
  const isAdmin = ref(null);
  const bootstrapping = ref(true);
  const error = ref("");
  const routePath = window.location.pathname.replace(/\/+$/, "") || "/";
  const standalonePaths = ["/login", "/password-reset", "/reset"];
  let bootstrapVersion = 0;

  async function bootstrap() {
    const version = ++bootstrapVersion;
    bootstrapping.value = true;
    authenticated.value = null;
    isAdmin.value = null;
    error.value = "";
    try {
      let response = await fetch("/api/v1/bootstrap/status", { cache: "no-store" });
      if (response.status === 404) {
        response = await fetch("/api/v1/setup/status", { cache: "no-store" });
      }
      let result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "读取初始化状态失败");
      if (result.mode === "bootstrap") {
        const fragment = new URLSearchParams(window.location.hash.slice(1));
        const token = fragment.get("token");
        if (token) {
          // 令牌不进入后续导航、浏览器历史或普通日志；兑换后仅保留 HttpOnly Cookie。
          window.history.replaceState(null, "", window.location.pathname + window.location.search);
          const exchange = await fetch("/api/v1/bootstrap/session", {
            method: "POST",
            credentials: "same-origin",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ token }),
          });
          if (!exchange.ok) throw new Error("初始化地址无效或已经过期，请在服务器上重新获取一次性地址。");
        }
        const settings = await fetch("/api/v1/bootstrap/settings", { cache: "no-store", credentials: "same-origin" });
        if (!settings.ok) throw new Error("请使用服务器 bootstrap-url 文件中的完整一次性地址访问，或在原浏览器中继续初始化。");
        result = await settings.json();
      }
      if (version !== bootstrapVersion) return;
      const session = result.initialized
        ? await checkAuth()
        : { authenticated: false, isAdmin: false };
      if (version !== bootstrapVersion) return;
      setupState.value = result;
      authenticated.value = session.authenticated;
      isAdmin.value = session.isAdmin;
    } catch (err) {
      if (version !== bootstrapVersion) return;
      setupState.value = null;
      authenticated.value = null;
      isAdmin.value = null;
      error.value = err.message || "无法连接核心服务，请先启动 scert serve";
    } finally {
      if (version === bootstrapVersion) bootstrapping.value = false;
    }
  }

  async function checkAuth() {
    const response = await fetch("/api/v1/auth/session", {
      credentials: "include",
      cache: "no-store",
    });
    if (response.status === 401) {
      return { authenticated: false, isAdmin: false };
    }
    const result = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(result.error || "读取登录状态失败");
    return {
      authenticated: result.authenticated === true,
      isAdmin: result.is_admin === true || ["super_admin", "admin"].includes(result.role),
    };
  }

  function handleInitialized(result) {
    if (result?.completed && result?.management_url) {
      let destination;
      try {
        destination = new URL(result.management_url);
      } catch {
        error.value = "正式管理地址无效，请检查服务器配置。";
        return;
      }
      if (destination.protocol !== "https:" || destination.username || destination.password) {
        error.value = "正式管理地址无效，请检查服务器配置。";
        return;
      }
      // 后端此时正在优雅切换监听端口，短暂等待后访问正式入口。
      window.setTimeout(() => window.location.assign(destination.href), 2000);
      return;
    }
    setupState.value = { ...setupState.value, initialized: true };
    authenticated.value = false;
    isAdmin.value = false;
  }

  function handlePageHide() {
    // 历史缓存不保留已验证权限，旧请求也不能恢复离开前的登录状态。
    bootstrapVersion += 1;
    authenticated.value = null;
    isAdmin.value = null;
    bootstrapping.value = true;
  }

  function handlePageShow(event) {
    if (event.persisted) bootstrap();
  }

  onMounted(() => {
    if (standalonePaths.includes(routePath)) {
      bootstrapping.value = false;
      return;
    }
    window.addEventListener("pagehide", handlePageHide);
    window.addEventListener("pageshow", handlePageShow);
    bootstrap();
  });

  onBeforeUnmount(() => {
    bootstrapVersion += 1;
    window.removeEventListener("pagehide", handlePageHide);
    window.removeEventListener("pageshow", handlePageShow);
  });

  return {
    setupState,
    authenticated,
    isAdmin,
    bootstrapping,
    error,
    routePath,
    bootstrap,
    handleInitialized,
  };
}
