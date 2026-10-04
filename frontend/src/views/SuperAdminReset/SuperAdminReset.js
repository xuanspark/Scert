import { ref } from "vue";

export function useSuperAdminReset() {
  const verificationCode = ref("");
  const password = ref("");
  const confirmPassword = ref("");
  const message = ref("当前方式无法使用，请联系运维人员辅助解决。");
  const error = ref("当前方式无法使用，请联系运维人员辅助解决。");
  const loading = ref(false);
  const submitting = ref(false);
  const success = ref(false);
  const redirectSeconds = ref(0);

  function loadSession() {
    return Promise.resolve();
  }

  function submit() {
    error.value = "当前方式无法使用，请联系运维人员辅助解决。";
  }

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
