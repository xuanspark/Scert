import { ref } from "vue";

export function usePasswordReset() {
  const identifier = ref("");
  const resolution = ref(null);
  const message = ref("");
  const error = ref("");
  const submitting = ref(false);

  async function submit() {
    if (submitting.value) {
      return;
    }
    message.value = "";
    error.value = "";
    resolution.value = null;
    if (!identifier.value.trim()) {
      error.value = "请输入用户名或绑定邮箱。";
      return;
    }

    submitting.value = true;
    try {
      const response = await fetch("/api/v1/password-reset/resolve", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json",
        },
        credentials: "same-origin",
        body: JSON.stringify({ identifier: identifier.value.trim() }),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(result.error || "暂时无法处理密码重置请求，请稍后重试。");
      }
      resolution.value = result;
      message.value = result.description || "已返回密码处理方式。";
    } catch (submitError) {
      error.value = submitError.message || "查询密码重置方式失败，请稍后重试。";
    } finally {
      submitting.value = false;
    }
  }

  function reset() {
    resolution.value = null;
    message.value = "";
    error.value = "";
  }

  return {
    identifier,
    resolution,
    message,
    error,
    submitting,
    submit,
    reset,
  };
}