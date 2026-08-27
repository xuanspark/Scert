import { onMounted, ref } from "vue";

export function useAppShell() {
  const setupState = ref(null);
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
    } catch (err) {
      error.value = err.message || "无法连接核心服务，请先启动 scert serve";
    } finally {
      bootstrapping.value = false;
    }
  }

  function handleInitialized() {
    setupState.value = { ...setupState.value, initialized: true };
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
    bootstrapping,
    error,
    routePath,
    bootstrap,
    handleInitialized,
  };
}
