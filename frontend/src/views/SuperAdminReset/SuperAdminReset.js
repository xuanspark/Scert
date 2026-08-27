import { onBeforeUnmount, onMounted, ref } from "vue";

function readResetMessage(html) {
  const documentNode = new DOMParser().parseFromString(html, "text/html");
  const notice = documentNode.querySelector(".notice");
  return notice?.textContent?.trim() || "重置请求未成功，请检查一次性校验码。";
}

export function useSuperAdminReset() {
  const verificationCode = ref("");
  const password = ref("");
  const confirmPassword = ref("");
  const csrfToken = ref("");
  const message = ref("");
  const error = ref("");
  const loading = ref(true);
  const submitting = ref(false);
  const success = ref(false);
  const redirectSeconds = ref(3);
  let redirectTimer;

  async function loadSession() {
    loading.value = true;
    error.value = "";
    try {
      const response = await fetch("/reset", {
        credentials: "same-origin",
        cache: "no-store",
        headers: {
          Accept: "application/x-scert-reset-bootstrap",
        },
      });
      const html = await response.text();
      if (!response.ok) {
        throw new Error("当前重置入口未开启，请先启动 Scert-admin。");
      }
      const documentNode = new DOMParser().parseFromString(html, "text/html");
      csrfToken.value = documentNode.querySelector(
        'input[name="csrf_token"]',
      )?.value || "";
      if (!csrfToken.value) {
        throw new Error("未能获取本次重置授权，请重新打开页面。");
      }
    } catch (loadError) {
      error.value = loadError.message || "无法获取重置授权，请稍后重试。";
    } finally {
      loading.value = false;
    }
  }

  function validate() {
    if (!verificationCode.value.trim()) {
      return "请输入一次性校验码。";
    }
    if (password.value.length < 8) {
      return "新密码至少需要 8 个字符。";
    }
    if (password.value !== confirmPassword.value) {
      return "两次输入的新密码不一致。";
    }
    return "";
  }

  async function submit() {
    if (submitting.value || success.value) {
      return;
    }
    message.value = "";
    error.value = validate();
    if (error.value) {
      return;
    }
    if (!csrfToken.value) {
      error.value = "本次重置授权已失效，请重新打开页面。";
      return;
    }

    submitting.value = true;
    try {
      const form = new URLSearchParams({
        csrf_token: csrfToken.value,
        reset_code: verificationCode.value.trim(),
        new_password: password.value,
        confirm_password: confirmPassword.value,
      });
      const response = await fetch("/reset", {
        method: "POST",
        credentials: "same-origin",
        headers: {
          Accept: "text/html",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: form,
      });
      const responseBody = await response.text();
      if (!response.ok) {
        throw new Error(readResetMessage(responseBody));
      }

      csrfToken.value = "";
      verificationCode.value = "";
      password.value = "";
      confirmPassword.value = "";
      success.value = true;
      redirectSeconds.value = 3;
      redirectTimer = window.setInterval(() => {
        redirectSeconds.value -= 1;
        if (redirectSeconds.value <= 0) {
          window.clearInterval(redirectTimer);
          window.location.href = "/login";
        }
      }, 1000);
    } catch (submitError) {
      error.value = submitError.message || "密码重置失败，请稍后重试。";
    } finally {
      submitting.value = false;
    }
  }

  onMounted(loadSession);

  onBeforeUnmount(() => {
    if (redirectTimer) {
      window.clearInterval(redirectTimer);
    }
  });

  return {
    verificationCode,
    password,
    confirmPassword,
    message,
    error,
    loading,
    submitting,
    success,
    redirectSeconds,
    submit,
    loadSession,
  };
}