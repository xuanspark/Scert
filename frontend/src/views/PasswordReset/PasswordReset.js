import { ref } from "vue";

export function usePasswordReset() {
  const username = ref("");
  const password = ref("");
  const confirmPassword = ref("");
  const message = ref("");
  const error = ref("");

  function submit() {
    message.value = "";
    error.value = "";
    if (password.value.length < 8) {
      error.value = "新密码至少需要 8 个字符。";
      return;
    }
    if (password.value !== confirmPassword.value) {
      error.value = "两次输入的新密码不一致。";
      return;
    }
    error.value = "普通管理员密码重置接口尚未接入，请联系超级管理员处理。";
  }

  return {
    username,
    password,
    confirmPassword,
    message,
    error,
    submit,
  };
}
