import { computed, ref } from "vue";

const roleLabels = {
  super_admin: "超级管理员",
  admin: "管理员",
  user: "普通用户",
};

export function useLogin() {
  const identifier = ref("");
  const password = ref("");
  const remember = ref(false);
  const submitting = ref(false);
  const error = ref("");
  const stage = ref("credentials");
  const accountRole = ref("");
  const accountName = ref("");

  const identifierLabel = "用户名或邮箱";
  const identifierPlaceholder = "请输入用户名或绑定邮箱";
  const stageTitle = computed(() => {
    if (stage.value === "totp") return "两步验证";
    if (stage.value === "external_email") return "邮箱验证";
    if (stage.value === "complete") {
      return `${roleLabels[accountRole.value] || "用户"}登录成功`;
    }
    return "用户登录";
  });
  const stageMessage = computed(() => {
    if (stage.value === "totp") return "该账户还需完成两步验证。";
    if (stage.value === "external_email") return "该账户还需完成第三方邮箱验证。";
    if (stage.value === "complete") return "正在进入 Scert 管理后台。";
    return "请输入用户名或邮箱。账户权限由服务端自动识别。";
  });

  function resetStage() {
    stage.value = "credentials";
    accountRole.value = "";
    accountName.value = "";
    error.value = "";
  }

  async function submit() {
    if (submitting.value || stage.value !== "credentials") return;
    error.value = "";
    if (!identifier.value.trim()) {
      error.value = "请输入用户名或邮箱。";
      return;
    }
    if (!password.value) {
      error.value = "请输入密码。";
      return;
    }

    submitting.value = true;
    try {
      const response = await fetch("/api/v1/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "include",
        body: JSON.stringify({
          identifier: identifier.value.trim(),
          password: password.value,
          remember: remember.value,
        }),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(result.error || "登录失败，请稍后重试");
      }

      accountRole.value = result.role || "";
      accountName.value = result.account || "";
      stage.value = result.next_step || "complete";
      if (stage.value === "complete") {
        window.setTimeout(() => window.location.assign("/"), 600);
      }
    } catch (err) {
      error.value = err.message || "登录失败，请稍后重试";
    } finally {
      submitting.value = false;
    }
  }

  return {
    identifier,
    password,
    remember,
    submitting,
    error,
    stage,
    accountName,
    identifierLabel,
    identifierPlaceholder,
    stageTitle,
    stageMessage,
    resetStage,
    submit,
  };
}
