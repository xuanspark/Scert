import { onMounted, ref } from "vue";

export function useAppShell() {
  const setupState = ref(null);
  const authenticated = ref(null);
  const isAdmin = ref(null);
  const bootstrapping = ref(true);
  const error = ref("");
  const routePath = window.location.pathname.replace(/\/+$/, "") || "/";
  const standalonePaths = ["/login", "/password-reset", "/reset"];

  async function bootstrap() {
    bootstrapping.value = true;
    error.value = "";
    try {
      const response = await fetch("/api/v1/setup/status");
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "读取初始化状态失败");
      setupState.value = result;
      if (result.initialized) {
        await checkAuth();
      } else {
        authenticated.value = false;
        isAdmin.value = false;
      }
    } catch (err) {
      setupState.value = null;
      authenticated.value = null;
      isAdmin.value = null;
      error.value = err.message || "无法连接核心服务，请先启动 scert serve";
    } finally {
      bootstrapping.value = false;
    }
  }

  async function checkAuth() {
    const response = await fetch("/api/v1/auth/session", {
      credentials: "include",
    });
    if (response.status === 401) {
      authenticated.value = false;
      isAdmin.value = false;
      return;
    }
    const result = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(result.error || "读取登录状态失败");
    authenticated.value = result.authenticated === true;
    isAdmin.value = result.is_admin === true || ["super_admin", "admin"].includes(result.role);
  }

  function handleInitialized() {
    setupState.value = { ...setupState.value, initialized: true };
    authenticated.value = false;
    isAdmin.value = false;
  }

  onMounted(() => {
    if (standalonePaths.includes(routePath)) {
      bootstrapping.value = false;
      return;
    }
    bootstrap();
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
