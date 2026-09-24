import { onBeforeUnmount, ref } from "vue";

export function useChangePassword() {
  const visible = ref(false);
  const submitting = ref(false);
  const success = ref(false);
  const error = ref("");
  const notice = ref("");
  const form = ref({
    currentPassword: "",
    newPassword: "",
    confirmPassword: "",
  });
  let redirectTimer = 0;

  function open() {
    visible.value = true;
    success.value = false;
    error.value = "";
    notice.value = "";
  }

  function close() {
    if (submitting.value || success.value) {
      return;
    }
    visible.value = false;
    error.value = "";
    notice.value = "";
    form.value = {
      currentPassword: "",
      newPassword: "",
      confirmPassword: "",
    };
  }

  async function submit() {
    if (submitting.value) {
      return;
    }
    error.value = "";
    notice.value = "";
    if (form.value.newPassword !== form.value.confirmPassword) {
      error.value = "两次输入的新密码不一致。";
      return;
    }
    submitting.value = true;
    try {
      const response = await fetch("/api/v1/auth/password", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Accept: "application/json",
        },
        credentials: "include",
        body: JSON.stringify({
          current_password: form.value.currentPassword,
          new_password: form.value.newPassword,
          confirm_password: form.value.confirmPassword,
        }),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(result.error || "修改密码失败，请稍后重试。");
      }
      success.value = true;
      notice.value = result.message || "密码修改成功，请重新登录。";
      redirectTimer = window.setTimeout(() => {
        window.location.href = "/login";
      }, 1600);
    } catch (submitError) {
      error.value = submitError.message || "修改密码失败，请稍后重试。";
    } finally {
      submitting.value = false;
    }
  }

  onBeforeUnmount(() => {
    if (redirectTimer) {
      window.clearTimeout(redirectTimer);
    }
  });

  return { visible, submitting, success, error, notice, form, open, close, submit };
}
