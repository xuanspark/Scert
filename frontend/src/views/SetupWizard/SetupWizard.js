import { computed, onMounted, onUnmounted, ref, watch } from "vue";

export function useSetupWizard(props, emit) {
  const scertLogo = "/Scert.png";

  const submitting = ref(false);
  const error = ref("");
  const resultMessage = ref("");
  const dnsCatalogLoading = ref(true);
  const dnsCatalogError = ref("");
  const dnsTesting = ref(false);
  const dnsTestError = ref("");
  const dnsProviders = ref([]);
  const dnsCredentials = ref({});
  const dnsTestDomain = ref(props.status.web_hostname || "");
  const dnsVerification = ref(null);
  const verificationClock = ref(Date.now());
  const currentStep = ref(1);
  let verificationTimer;
  const targetType = ref(props.status.web_public_ip ? "ip" : "hostname");
  const form = ref({
    admin_username: "",
    admin_email: "",
    admin_password: "",
    confirm_password: "",
    server_port: props.status.server_port || 8080,
    email_provider: "",
    email_endpoint: "",
    recipients: "",
    dns_provider: props.status.dns_provider || "cloudflare",
    certificate_root: props.status.certificate_root || "",
    web_hostname: props.status.web_hostname || "",
    web_public_ip: props.status.web_public_ip || "",
    web_enabled: props.status.web_enabled !== false,
    https_mode: props.status.https_mode || "direct",
  });

  const targetLabel = computed(() =>
    targetType.value === "hostname" ? "管理页面域名" : "公网 IP 地址",
  );
  const resetURLPreview = computed(() => {
    const rawHost =
      targetType.value === "hostname"
        ? form.value.web_hostname
        : form.value.web_public_ip;
    const host = String(rawHost || "")
      .trim()
      .replace(/^\[|\]$/g, "");
    if (!host) return "完成域名或公网 IP 配置后自动生成";
    const displayHost = host.includes(":") ? `[${host}]` : host;
    return `https://${displayHost}/reset`;
  });
  const selectedDNSProvider = computed(
    () =>
      dnsProviders.value.find(
        (provider) => provider.id === form.value.dns_provider,
      ) || null,
  );
  const dnsVerificationValid = computed(() => {
    verificationClock.value;
    if (
      !dnsVerification.value?.verification_token ||
      !dnsVerification.value?.credential_ref
    )
      return false;
    return new Date(dnsVerification.value.expires_at).getTime() > Date.now();
  });

  function invalidateDNSVerification() {
    dnsVerification.value = null;
    dnsTestError.value = "";
  }

  function formatVerificationExpiry(value) {
    if (!value) return "";
    return new Intl.DateTimeFormat("zh-CN", {
      dateStyle: "medium",
      timeStyle: "medium",
      timeZone: "Asia/Shanghai",
    }).format(new Date(value));
  }

  async function loadDNSProviders() {
    dnsCatalogLoading.value = true;
    dnsCatalogError.value = "";
    try {
      const response = await fetch("/api/v1/setup/dns/providers");
      const result = await response.json().catch(() => ({}));
      if (!response.ok)
        throw new Error(result.error || "读取 DNS 服务商列表失败");
      dnsProviders.value = Array.isArray(result.items) ? result.items : [];
      if (
        !dnsProviders.value.some(
          (provider) => provider.id === form.value.dns_provider,
        )
      ) {
        form.value.dns_provider =
          dnsProviders.value.find((provider) => provider.available)?.id || "";
      }
    } catch (err) {
      dnsCatalogError.value = err.message || "读取 DNS 服务商列表失败";
    } finally {
      dnsCatalogLoading.value = false;
    }
  }

  function credentialPayload() {
    const payload = {};
    for (const field of selectedDNSProvider.value?.credential_fields || []) {
      const value = String(dnsCredentials.value[field.name] || "").trim();
      if (value) payload[field.name] = value;
    }
    return payload;
  }

  async function testDNS() {
    dnsTestError.value = "";
    resultMessage.value = "";
    invalidateDNSVerification();
    const provider = selectedDNSProvider.value;
    if (!provider) {
      dnsTestError.value = "请选择 DNS 服务商。";
      return;
    }
    if (!provider.available) {
      dnsTestError.value = "该 DNS 服务商在线验证适配器尚未开放。";
      return;
    }
    const testDomain = dnsTestDomain.value.trim();
    if (!testDomain) {
      dnsTestError.value = "请填写 DNS 权限测试域名。";
      return;
    }
    for (const field of provider.credential_fields || []) {
      if (
        field.required &&
        !String(dnsCredentials.value[field.name] || "").trim()
      ) {
        dnsTestError.value = `请填写${field.label}。`;
        return;
      }
    }

    dnsTesting.value = true;
    try {
      const response = await fetch("/api/v1/setup/dns/test", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          provider: provider.id,
          test_domain: testDomain,
          credentials: credentialPayload(),
        }),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "DNS API 在线验证失败");
      dnsVerification.value = result;
    } catch (err) {
      dnsTestError.value = err.message || "DNS API 在线验证失败";
    } finally {
      dnsTesting.value = false;
    }
  }

  function validateCurrentStep() {
    error.value = "";
    resultMessage.value = "";

    if (currentStep.value === 1) {
      if (!form.value.admin_username.trim()) {
        error.value = "请输入超级管理员用户名。";
        return false;
      }
      if (!form.value.admin_email.trim()) {
        error.value = "请输入超级管理员绑定邮箱。";
        return false;
      }
      if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(form.value.admin_email.trim())) {
        error.value = "请输入合法的超级管理员绑定邮箱。";
        return false;
      }
      if (form.value.admin_password.length < 8) {
        error.value = "超级管理员密码至少需要 8 个字符。";
        return false;
      }
      if (form.value.admin_password !== form.value.confirm_password) {
        error.value = "两次输入的超级管理员密码不一致。";
        return false;
      }
    }

    if (currentStep.value === 2) {
      const port = Number(form.value.server_port);
      if (!Number.isInteger(port) || port < 1 || port > 65535) {
        error.value = "请输入 1 至 65535 之间的主服务端口。";
        return false;
      }
    }

    if (currentStep.value === 3) {
      const provider = selectedDNSProvider.value;
      if (!provider) {
        error.value = "请选择 DNS 服务商。";
        return false;
      }
      if (!provider.available) {
        error.value = "该 DNS 服务商在线验证适配器尚未开放。";
        return false;
      }
      if (!dnsTestDomain.value.trim()) {
        error.value = "请填写 DNS 权限测试域名。";
        return false;
      }
      for (const field of provider.credential_fields || []) {
        if (
          field.required &&
          !String(dnsCredentials.value[field.name] || "").trim()
        ) {
          error.value = `请填写${field.label}。`;
          return false;
        }
      }
      if (!dnsVerificationValid.value) {
        error.value = "请先完成 DNS API 在线验证。";
        return false;
      }
    }

    if (currentStep.value === 4) {
      const hostname =
        targetType.value === "hostname" ? form.value.web_hostname.trim() : "";
      const publicIP =
        targetType.value === "ip" ? form.value.web_public_ip.trim() : "";
      if (!hostname && !publicIP) {
        error.value = `请填写${targetLabel.value}。`;
        return false;
      }
      if (!form.value.certificate_root.trim()) {
        error.value = "请填写证书部署根目录。";
        return false;
      }
    }

    return true;
  }

  function nextStep() {
    if (!validateCurrentStep()) return;
    if (currentStep.value < 4) currentStep.value += 1;
  }

  function previousStep() {
    error.value = "";
    resultMessage.value = "";
    if (currentStep.value > 1) currentStep.value -= 1;
  }

  function handleSubmit() {
    if (currentStep.value < 4) {
      nextStep();
      return;
    }
    submit();
  }

  function recipients() {
    return form.value.recipients
      .split(/[\n,，]/)
      .map((item) => item.trim())
      .filter(Boolean);
  }

  async function submit() {
    error.value = "";
    resultMessage.value = "";
    if (form.value.admin_password.length < 8) {
      error.value = "超级管理员密码至少需要 8 个字符。";
      return;
    }
    if (form.value.admin_password !== form.value.confirm_password) {
      error.value = "两次输入的超级管理员密码不一致。";
      return;
    }
    if (!dnsVerificationValid.value) {
      error.value = "必须先完成 DNS API 在线验证；验证结果已过期时请重新测试。";
      return;
    }
    const hostname =
      targetType.value === "hostname" ? form.value.web_hostname.trim() : "";
    const publicIP =
      targetType.value === "ip" ? form.value.web_public_ip.trim() : "";
    if (!hostname && !publicIP) {
      error.value = `请填写${targetLabel.value}。`;
      return;
    }

    submitting.value = true;
    try {
      const response = await fetch("/api/v1/setup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          admin_username: form.value.admin_username,
          admin_email: form.value.admin_email,
          admin_password: form.value.admin_password,
          server_port: Number(form.value.server_port),
          email_provider: form.value.email_provider,
          email_endpoint: form.value.email_endpoint,
          recipients: recipients(),
          dns_provider: form.value.dns_provider,
          dns_credential_ref: dnsVerification.value.credential_ref,
          dns_verification_token: dnsVerification.value.verification_token,
          dns_test_domain: dnsTestDomain.value.trim(),
          certificate_root: form.value.certificate_root,
          web_hostname: hostname,
          web_public_ip: publicIP,
          web_enabled: form.value.web_enabled,
          https_mode: form.value.https_mode,
        }),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "保存初始化配置失败");
      resultMessage.value = result.message || "初始化配置已保存。";
      emit("initialized", result);
    } catch (err) {
      error.value = err.message || "保存初始化配置失败";
    } finally {
      submitting.value = false;
    }
  }

  watch(
    () => form.value.dns_provider,
    () => {
      dnsCredentials.value = {};
      invalidateDNSVerification();
    },
  );

  watch(dnsCredentials, invalidateDNSVerification, { deep: true });
  watch(dnsTestDomain, invalidateDNSVerification);

  onMounted(() => {
    loadDNSProviders();
    verificationTimer = window.setInterval(() => {
      verificationClock.value = Date.now();
    }, 30000);
  });

  onUnmounted(() => {
    if (verificationTimer) window.clearInterval(verificationTimer);
  });

  return {
    scertLogo,
    submitting,
    error,
    resultMessage,
    dnsCatalogLoading,
    dnsCatalogError,
    dnsTesting,
    dnsTestError,
    dnsProviders,
    dnsCredentials,
    dnsTestDomain,
    dnsVerification,
    currentStep,
    targetType,
    form,
    targetLabel,
    resetURLPreview,
    selectedDNSProvider,
    dnsVerificationValid,
    formatVerificationExpiry,
    testDNS,
    handleSubmit,
    previousStep,
  };
}
