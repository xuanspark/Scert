import { computed, ref } from "vue";

const roleLabels = {
  super_admin: "\u8d85\u7ea7\u7ba1\u7406\u5458",
  admin: "\u7ba1\u7406\u5458",
  user: "\u666e\u901a\u7528\u6237",
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

  const identifierLabel = "\u7528\u6237\u540d\u6216\u90ae\u7bb1";
  const identifierPlaceholder = "\u8bf7\u8f93\u5165\u7528\u6237\u540d\u6216\u7ed1\u5b9a\u90ae\u7bb1";
  const stageTitle = computed(() => {
    if (stage.value === "totp") return "\u4e24\u6b65\u9a8c\u8bc1";
    if (stage.value === "external_email") return "\u90ae\u7bb1\u9a8c\u8bc1";
    if (stage.value === "complete") {
      return `${roleLabels[accountRole.value] || "\u7528\u6237"}\u767b\u5f55\u6210\u529f`;
    }
    return "\u7ba1\u7406\u5458\u767b\u5f55";
  });
  const stageMessage = computed(() => {
    if (stage.value === "totp") return "\u8be5\u8d26\u6237\u8fd8\u9700\u5b8c\u6210\u4e24\u6b65\u9a8c\u8bc1\u3002";
    if (stage.value === "external_email") return "\u8be5\u8d26\u6237\u8fd8\u9700\u5b8c\u6210\u7b2c\u4e09\u65b9\u90ae\u7bb1\u9a8c\u8bc1\u3002";
    if (stage.value === "complete") return "\u6b63\u5728\u8fdb\u5165 Scert \u7ba1\u7406\u540e\u53f0\u3002";
    return "\u8bf7\u8f93\u5165\u7528\u6237\u540d\u6216\u90ae\u7bb1\u3002\u8d26\u6237\u6743\u9650\u7531\u670d\u52a1\u7aef\u81ea\u52a8\u8bc6\u522b\u3002";
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
      error.value = "\u8bf7\u8f93\u5165\u7528\u6237\u540d\u6216\u90ae\u7bb1\u3002";
      return;
    }
    if (!password.value) {
      error.value = "\u8bf7\u8f93\u5165\u5bc6\u7801\u3002";
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
        throw new Error(result.error || "\u767b\u5f55\u5931\u8d25\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5");
      }

      accountRole.value = result.role || "";
      accountName.value = result.account || "";
      stage.value = result.next_step || "complete";
      if (stage.value === "complete") {
        window.setTimeout(() => window.location.assign("/"), 600);
      }
    } catch (err) {
      error.value = err.message || "\u767b\u5f55\u5931\u8d25\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5";
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
