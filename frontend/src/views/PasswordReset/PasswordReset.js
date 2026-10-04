import { ref } from "vue";

export function usePasswordReset() {
  const identifier = ref("");
  const resolution = ref({
    action: "contact_administrator",
    title: "密码重置",
    description: "当前方式无法使用，请联系运维人员辅助解决。",
  });
  const message = ref("当前方式无法使用，请联系运维人员辅助解决。");
  const error = ref("");
  const submitting = ref(false);

  function submit() {
    error.value = "当前方式无法使用，请联系运维人员辅助解决。";
  }

  function reset() {
    identifier.value = "";
    resolution.value = {
      action: "contact_administrator",
      title: "密码重置",
      description: "当前方式无法使用，请联系运维人员辅助解决。",
    };
    message.value = "当前方式无法使用，请联系运维人员辅助解决。";
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
