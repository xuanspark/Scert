import { ref } from "vue";

export function useAccessDenied() {
  const loggingOut = ref(false);
  const error = ref("");

  async function logout() {
    if (loggingOut.value) return;
    loggingOut.value = true;
    error.value = "";
    try {
      const response = await fetch("/api/v1/auth/logout", {
        method: "POST",
        credentials: "include",
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(result.error || "退出登录失败");
      }
      window.location.assign("/login");
    } catch (err) {
      error.value = err.message || "退出登录失败";
    } finally {
      loggingOut.value = false;
    }
  }

  return { loggingOut, error, logout };
}
