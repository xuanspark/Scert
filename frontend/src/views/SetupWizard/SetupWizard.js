import { computed, onMounted, onUnmounted, ref, watch } from "vue";

const defaultDNSVerificationTimeoutSeconds = 60;
const aliyunDNSVerificationTimeoutSeconds = 10;

function dnsVerificationTimeoutFor(providerID) {
  return String(providerID || "").trim().toLowerCase() === "aliyun"
    ? aliyunDNSVerificationTimeoutSeconds
    : defaultDNSVerificationTimeoutSeconds;
}

const commonEmailDomains = [
  { label: "常用邮箱", value: "" },
  { label: "@163.com", value: "163.com" },
  { label: "@qq.com", value: "qq.com" },
  { label: "@126.com", value: "126.com" },
  { label: "@outlook.com", value: "outlook.com" },
  { label: "@gmail.com", value: "gmail.com" },
  { label: "@foxmail.com", value: "foxmail.com" },
  { label: "@aliyun.com", value: "aliyun.com" },
  { label: "@139.com", value: "139.com" },
  { label: "@189.cn", value: "189.cn" },
  { label: "自定义后缀", value: "custom" },
];


export function useSetupWizard(props, emit) {
  const scertLogo = "/Scert.png";

  const submitting = ref(false);
  const error = ref("");
  const resultMessage = ref("");
  const dnsCatalogLoading = ref(true);
  const dnsCatalogError = ref("");
  const dnsTesting = ref(false);
  const dnsTestError = ref("");
  const dnsVerificationElapsedSeconds = ref(0);
  const dnsVerificationTimeoutSeconds = ref(defaultDNSVerificationTimeoutSeconds);
  const dnsProviders = ref([]);
  const dnsCredentials = ref({});
  const dnsCredentialsVisible = ref(true);
  const emailCredentials = ref("");
  const emailCredentialsVisible = ref(true);
  const dnsTestDomain = ref(props.status.web_hostname || "");
  const dnsVerification = ref(null);
  const verificationClock = ref(Date.now());
  const currentStep = ref(1);
  const notificationAdvancedVisible = ref(false);
  const selectedEmailDomain = ref("");
  const emailLocalPart = ref("");
  const emailInputElement = ref(null);
  const emailAutofillElement = ref(null);
  let emailVisibleEdited = false;
  let verificationTimer;
  let dnsVerificationTimer;
  let dnsRequestVersion = 0;
  let emailAutofillTimer;
  const targetType = ref(props.status.web_public_ip ? "ip" : "hostname");
  const form = ref({
    admin_username: "",
    admin_email: "",
    admin_password: "",
    confirm_password: "",
    server_port: props.status.server_port || 8080,
    email_http_enabled: false,
    email_smtp_enabled: false,
    email_smtp_host: "",
    email_smtp_port: 587,
    email_smtp_username: "",
    email_smtp_password: "",
    email_smtp_mode: "starttls",
    email_smtp_allow_plaintext_auth: false,
    email_provider: "",
    email_endpoint: "",
    email_http_path: "/api/email/send",
    email_http_login_path: "/api/login",
    email_http_auth_mode: "none",
    email_http_credential_ref: "",
    email_http_headers: "",
    email_event_mode: "detailed",
    email_include_deployment_details: true,
    email_include_certificate_serial: true,
    email_include_management_url: true,
    email_sender_name: "Scert",
    email_sender_email: "",
    email_timeout_secs: 10,
    email_retry_count: 3,
    email_retry_intervals_secs: "30,120,600",
    recipients: "",
    dns_provider: props.status.dns_provider || "aliyun",
    propagation_timeout_secs: props.status.propagation_timeout_secs || 900,
    propagation_polling_interval_secs:
      props.status.propagation_polling_interval_secs || 10,
    certificate_root: props.status.certificate_root || "",
    web_hostname: props.status.web_hostname || "",
    web_public_ip: props.status.web_public_ip || "",
    web_enabled: props.status.web_enabled !== false,
    https_mode: props.status.https_mode || "direct",
  });

  const targetLabel = computed(() =>
    targetType.value === "hostname" ? "管理页面域名" : "公网 IP 地址",
  );
  const emailCredentialLabel = computed(() => {
    switch (form.value.email_http_auth_mode) {
      case "bearer":
        return "Bearer Token 凭据（JSON）";
      case "hmac_sha256":
        return "HMAC-SHA256 凭据（JSON）";
      case "session":
        return "登录会话凭据（JSON）";
      default:
        return "HTTP 通知凭据";
    }
  });
  const emailCredentialPlaceholder = computed(() => {
    switch (form.value.email_http_auth_mode) {
      case "bearer":
        return '{"token":"请输入 Bearer Token"}';
      case "hmac_sha256":
        return '{"secret":"请输入签名密钥"}';
      case "session":
        return '{"account":"请输入账号","password":"请输入密码"}';
      default:
        return "无需填写凭据";
    }
  });
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
    dnsRequestVersion += 1;
    stopDNSVerificationTimer();
    dnsTesting.value = false;
    dnsVerification.value = null;
    dnsTestError.value = "";
  }

  function updateDNSVerificationTimeoutSeconds(value) {
    const timeout = Number(value);
    if (Number.isFinite(timeout) && timeout > 0) {
      dnsVerificationTimeoutSeconds.value = Math.floor(timeout);
    }
  }

  function stopDNSVerificationTimer(requestVersion) {
    if (requestVersion !== undefined && requestVersion !== dnsRequestVersion) return;
    if (dnsVerificationTimer) {
      window.clearInterval(dnsVerificationTimer);
      dnsVerificationTimer = undefined;
    }
    dnsVerificationElapsedSeconds.value = 0;
  }

  function startDNSVerificationTimer(requestVersion, providerID) {
    stopDNSVerificationTimer();
    dnsVerificationTimeoutSeconds.value = dnsVerificationTimeoutFor(providerID);
    const startedAt = Date.now();
    const updateElapsedSeconds = () => {
      if (requestVersion !== dnsRequestVersion) return;
      dnsVerificationElapsedSeconds.value = Math.max(
        0,
        Math.floor((Date.now() - startedAt) / 1000),
      );
    };
    updateElapsedSeconds();
    dnsVerificationTimer = window.setInterval(updateElapsedSeconds, 1000);
  }

  function formatVerificationExpiry(value) {
    if (!value) return "";
    return new Intl.DateTimeFormat("zh-CN", {
      dateStyle: "medium",
      timeStyle: "medium",
      timeZone: "Asia/Shanghai",
    }).format(new Date(value));
  }

  function formatVerificationStageStatus(status) {
    switch (status) {
      case "completed":
        return "已完成";
      case "warning":
        return "警告";
      case "failed":
        return "失败";
      default:
        return "处理中";
    }
  }

  function findCommonEmailDomain(domain) {
    const normalizedDomain = String(domain || "").trim().toLowerCase();
    return commonEmailDomains.find(
      (option) =>
        option.value &&
        option.value !== "custom" &&
        option.value === normalizedDomain,
    );
  }

  function getEmailLocalPart(value) {
    const text = String(value || "").trim();
    const atIndex = text.indexOf("@");
    return (atIndex >= 0 ? text.slice(0, atIndex) : text).trim();
  }

  function isCompleteEmail(localPart, domain) {
    return /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(
      `${localPart}@${domain}`,
    );
  }
  function updateEmailAutofillValue(value) {
    if (!emailAutofillElement.value) return;
    emailAutofillElement.value.value = String(value || "");
  }

  function handleEmailInput(value) {
    emailVisibleEdited = true;
    error.value = "";
    const rawValue = String(value || "").trim();

    if (!rawValue) {
      emailLocalPart.value = "";
      selectedEmailDomain.value = "";
      form.value.admin_email = "";
      updateEmailAutofillValue("");
      return;
    }

    const atIndex = rawValue.indexOf("@");
    if (atIndex < 0) {
      emailLocalPart.value = rawValue;
      form.value.admin_email = selectedEmailDomain.value && selectedEmailDomain.value !== "custom"
        ? `${rawValue}@${selectedEmailDomain.value}`
        : rawValue;
      updateEmailAutofillValue(form.value.admin_email);
      return;
    }

    const localPart = rawValue.slice(0, atIndex).trim();
    const domain = rawValue.slice(atIndex + 1).trim().toLowerCase();

    if (!localPart || !domain) {
      emailLocalPart.value = rawValue;
      selectedEmailDomain.value = domain ? "custom" : "";
      form.value.admin_email = rawValue;
      updateEmailAutofillValue(form.value.admin_email);
      return;
    }

    const commonDomain = findCommonEmailDomain(domain);
    if (commonDomain) {
      emailLocalPart.value = localPart;
      selectedEmailDomain.value = commonDomain.value;
      form.value.admin_email = `${localPart}@${commonDomain.value}`;
      updateEmailAutofillValue(form.value.admin_email);
      return;
    }

    emailLocalPart.value = rawValue;
    selectedEmailDomain.value = "";
    form.value.admin_email = rawValue;
    updateEmailAutofillValue(form.value.admin_email);
  }

  function synchronizeEmailField(value) {
    const visibleValue = emailInputElement.value?.value?.trim() || "";
    const autofillValue = emailAutofillElement.value?.value?.trim() || "";
    const inputValue =
      value !== undefined
        ? value
        : visibleValue.includes("@")
          ? visibleValue
          : emailVisibleEdited
            ? visibleValue
            : autofillValue.includes("@")
              ? autofillValue
              : visibleValue;
    const rawValue = String(inputValue || "").trim();

    if (!rawValue) return;

    if (rawValue.includes("@") || rawValue !== emailLocalPart.value) {
      handleEmailInput(rawValue);
      return;
    }

    if (
      selectedEmailDomain.value &&
      selectedEmailDomain.value !== "custom"
    ) {
      form.value.admin_email = `${rawValue}@${selectedEmailDomain.value}`;
      updateEmailAutofillValue(form.value.admin_email);
    }
  }
  function handleEmailAutofill() {
    synchronizeEmailField();
  }

  function handleEmailPaste(event) {
    const pastedValue = event.clipboardData?.getData("text") || "";
    if (!pastedValue.includes("@")) return;

    event.preventDefault();
    handleEmailInput(pastedValue);
    handleEmailBlur();
  }

  function handleEmailBlur(value) {
    synchronizeEmailField(value);
    const rawValue = String(emailLocalPart.value || "").trim();
    const atIndex = rawValue.indexOf("@");
    if (atIndex < 0) {
      handleEmailInput(rawValue);
      return;
    }

    const localPart = rawValue.slice(0, atIndex).trim();
    const domain = rawValue.slice(atIndex + 1).trim().toLowerCase();
    if (localPart && isCompleteEmail(localPart, domain)) {
      emailLocalPart.value = localPart;
      selectedEmailDomain.value = findCommonEmailDomain(domain)?.value || "custom";
      form.value.admin_email = `${localPart}@${domain}`;
      updateEmailAutofillValue(form.value.admin_email);
    }
  }

  function applyEmailDomain(domain) {
    error.value = "";
    if (!domain || domain === "custom") return;

    const currentEmail = form.value.admin_email.trim();
    const localPart =
      getEmailLocalPart(emailLocalPart.value) || getEmailLocalPart(currentEmail);
    if (!localPart) {
      selectedEmailDomain.value = "";
      error.value = "请先输入邮箱名称，再选择常用邮箱。";
      return;
    }

    emailLocalPart.value = localPart;
    form.value.admin_email = `${localPart}@${domain}`;
    updateEmailAutofillValue(form.value.admin_email);
  }

  function toggleNotificationAdvanced() {
    notificationAdvancedVisible.value = !notificationAdvancedVisible.value;
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

  function maskCredential(value) {
    const text = String(value || "");
    if (!text) return "";
    return "•".repeat(Math.min(Math.max(text.length, 8), 64));
  }

  function emailCredentialPayload() {
    if (
      !form.value.email_http_enabled ||
      form.value.email_http_auth_mode === "none"
    ) {
      return {};
    }
    const text = emailCredentials.value.trim();
    if (!text) return {};
    const parsed = JSON.parse(text);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
      throw new Error("HTTP 通知凭据必须是 JSON 对象。");
    }
    for (const [key, value] of Object.entries(parsed)) {
      if (!key.trim() || typeof value !== "string" || !value.trim()) {
        throw new Error("HTTP 通知凭据的字段和值必须是非空文本。");
      }
    }
    return parsed;
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

    const requestVersion = dnsRequestVersion;
    dnsTesting.value = true;
    startDNSVerificationTimer(requestVersion, provider.id);
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
      if (requestVersion !== dnsRequestVersion) return;
      updateDNSVerificationTimeoutSeconds(result.verification_timeout_secs);
      if (result.test_report) {
        dnsVerification.value = { test_report: result.test_report };
      }
      if (!response.ok) throw new Error(result.error || "DNS API 在线验证失败");
      dnsVerification.value = result;
    } catch (err) {
      if (requestVersion !== dnsRequestVersion) return;
      dnsTestError.value = err.message || "DNS API 在线验证失败";
    } finally {
      if (requestVersion === dnsRequestVersion) {
        stopDNSVerificationTimer(requestVersion);
        dnsTesting.value = false;
      }
    }
  }

  function validateCurrentStep() {
    if (currentStep.value === 1) synchronizeEmailField();
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
      if (
        form.value.admin_password.length < 8 ||
        form.value.admin_password.length > 12
      ) {
        error.value = "超级管理员密码长度必须为 8 至 12 个字符。";
        return false;
      }
      if (form.value.admin_password !== form.value.confirm_password) {
        error.value = "两次输入的超级管理员密码不一致。";
        return false;
      }
      const port = Number(form.value.server_port);
      if (!Number.isInteger(port) || port < 1 || port > 65535) {
        error.value = "请输入 1 至 65535 之间的主服务端口。";
        return false;
      }
      const hostname =
        targetType.value === "hostname" ? form.value.web_hostname.trim() : "";
      const publicIP =
        targetType.value === "ip" ? form.value.web_public_ip.trim() : "";
      if (!hostname && !publicIP) {
        error.value = `请填写${targetLabel.value}。`;
        return false;
      }
    }

    if (currentStep.value === 2) {
      if (form.value.email_smtp_enabled) {
        if (!form.value.email_smtp_host.trim()) {
          error.value = "启用 SMTP 通知后必须填写 SMTP 主机。";
          return false;
        }
        const smtpPort = Number(form.value.email_smtp_port);
        if (!Number.isInteger(smtpPort) || smtpPort < 1 || smtpPort > 65535) {
          error.value = "SMTP 端口必须在 1 至 65535 之间。";
          return false;
        }
        if (!form.value.email_smtp_username.trim()) {
          error.value = "启用 SMTP 通知后必须填写 SMTP 用户名。";
          return false;
        }
        if (!form.value.email_smtp_password) {
          error.value = "启用 SMTP 通知后必须填写 SMTP 密码。";
          return false;
        }
        if (!["plain", "starttls", "implicit_tls"].includes(form.value.email_smtp_mode)) {
          error.value = "SMTP 连接模式无效。";
          return false;
        }
        if (form.value.email_smtp_mode === "plain" && !form.value.email_smtp_allow_plaintext_auth) {
          error.value = "SMTP 明文模式必须明确允许明文认证，建议改用 STARTTLS 或隐式 TLS。";
          return false;
        }
      }
      if (form.value.email_http_enabled && !form.value.email_endpoint.trim()) {
        error.value = "启用 HTTP 邮件通知后必须填写 API 地址。";
        return false;
      }
      if (
        form.value.email_http_enabled &&
        form.value.email_http_auth_mode !== "none" &&
        !form.value.email_http_credential_ref.trim() &&
        !emailCredentials.value.trim()
      ) {
        error.value = "启用 HTTP 通知认证后必须填写明文凭据或已有凭据引用。";
        return false;
      }
      if (
        form.value.email_http_enabled &&
        form.value.email_http_auth_mode !== "none" &&
        emailCredentials.value.trim()
      ) {
        try {
          emailCredentialPayload();
        } catch (err) {
          error.value = err.message || "HTTP 通知凭据格式无效。";
          return false;
        }
      }
      if (form.value.email_http_headers.trim()) {
        try {
          const headers = JSON.parse(form.value.email_http_headers);
          if (!headers || Array.isArray(headers) || typeof headers !== "object") {
            throw new Error("not-object");
          }
        } catch {
          error.value = "HTTP 自定义请求头必须是 JSON 对象。";
          return false;
        }
      }
    }

    if (currentStep.value === 3) {
      const propagationTimeout = Number(form.value.propagation_timeout_secs);
      const propagationInterval = Number(
        form.value.propagation_polling_interval_secs,
      );
      if (
        !Number.isInteger(propagationTimeout) ||
        propagationTimeout < 1 ||
        propagationTimeout > 86400
      ) {
        error.value = "DNS 传播最大等待时间必须在 1 至 86400 秒之间。";
        return false;
      }
      if (
        !Number.isInteger(propagationInterval) ||
        propagationInterval < 1 ||
        propagationInterval > propagationTimeout
      ) {
        error.value = "DNS 传播检测间隔必须大于 0 且不超过最大等待时间。";
        return false;
      }
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

  function emailHeaders() {
    if (!form.value.email_http_headers.trim()) return {};
    return JSON.parse(form.value.email_http_headers);
  }

  function retryIntervals() {
    return form.value.email_retry_intervals_secs
      .split(/[,，]/)
      .map((item) => Number(item.trim()))
      .filter((item) => Number.isInteger(item) && item > 0);
  }

  async function submit() {
    error.value = "";
    resultMessage.value = "";
    if (
      form.value.admin_password.length < 8 ||
      form.value.admin_password.length > 12
    ) {
      error.value = "超级管理员密码长度必须为 8 至 12 个字符。";
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
    if (form.value.email_smtp_enabled) {
      const smtpPort = Number(form.value.email_smtp_port);
      if (!form.value.email_smtp_host.trim()) {
        error.value = "启用 SMTP 通知后必须填写 SMTP 主机。";
        return;
      }
      if (!Number.isInteger(smtpPort) || smtpPort < 1 || smtpPort > 65535) {
        error.value = "SMTP 端口必须在 1 至 65535 之间。";
        return;
      }
      if (!form.value.email_smtp_username.trim()) {
        error.value = "启用 SMTP 通知后必须填写 SMTP 用户名。";
        return;
      }
      if (!form.value.email_smtp_password) {
        error.value = "启用 SMTP 通知后必须填写 SMTP 密码。";
        return;
      }
      if (form.value.email_smtp_mode === "plain" && !form.value.email_smtp_allow_plaintext_auth) {
        error.value = "SMTP 明文模式必须明确允许明文认证，建议改用 STARTTLS 或隐式 TLS。";
        return;
      }
    }
    try {
      emailCredentialPayload();
    } catch (err) {
      error.value = err.message || "HTTP 通知凭据格式无效。";
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
          email_http_enabled: form.value.email_http_enabled,
          email_smtp_enabled: form.value.email_smtp_enabled,
          email_smtp_host: form.value.email_smtp_host,
          email_smtp_port: Number(form.value.email_smtp_port),
          email_smtp_username: form.value.email_smtp_username,
          email_smtp_password: form.value.email_smtp_password,
          email_smtp_mode: form.value.email_smtp_mode,
          email_smtp_allow_plaintext_auth: form.value.email_smtp_allow_plaintext_auth,
          email_provider: form.value.email_provider,
          email_endpoint: form.value.email_endpoint,
          email_http_path: form.value.email_http_path,
          email_http_login_path: form.value.email_http_login_path,
          email_http_auth_mode: form.value.email_http_auth_mode,
          email_http_credential_ref: form.value.email_http_credential_ref,
          email_http_credentials: emailCredentialPayload(),
          email_http_headers: emailHeaders(),
          email_event_mode: form.value.email_event_mode,
          email_include_deployment_details:
            form.value.email_include_deployment_details,
          email_include_certificate_serial:
            form.value.email_include_certificate_serial,
          email_include_management_url: form.value.email_include_management_url,
          email_sender_name: form.value.email_sender_name,
          email_sender_email: form.value.email_sender_email,
          email_timeout_secs: Number(form.value.email_timeout_secs),
          email_retry_count: Number(form.value.email_retry_count),
          email_retry_intervals_secs: retryIntervals(),
          recipients: recipients(),
          dns_provider: form.value.dns_provider,
          dns_credential_ref: dnsVerification.value.credential_ref,
          dns_verification_token: dnsVerification.value.verification_token,
          dns_test_domain: dnsTestDomain.value.trim(),
          propagation_timeout_secs: Number(
            form.value.propagation_timeout_secs,
          ),
          propagation_polling_interval_secs: Number(
            form.value.propagation_polling_interval_secs,
          ),
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
    { flush: "sync" },
  );

  watch(dnsCredentials, invalidateDNSVerification, { deep: true, flush: "sync" });
  watch(dnsTestDomain, invalidateDNSVerification, { flush: "sync" });

  onMounted(() => {
    loadDNSProviders();
    emailAutofillTimer = window.setInterval(() => {
      if (currentStep.value === 1) synchronizeEmailField();
    }, 250);
    verificationTimer = window.setInterval(() => {
      verificationClock.value = Date.now();
    }, 30000);
  });

  onUnmounted(() => {
    invalidateDNSVerification();
    if (verificationTimer) window.clearInterval(verificationTimer);
    if (emailAutofillTimer) window.clearInterval(emailAutofillTimer);
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
    dnsVerificationElapsedSeconds,
    dnsVerificationTimeoutSeconds,
    dnsProviders,
    dnsCredentials,
    dnsCredentialsVisible,
    maskCredential,
    emailCredentials,
    emailCredentialsVisible,
    dnsTestDomain,
    dnsVerification,
    currentStep,
    notificationAdvancedVisible,
    targetType,
    form,
    commonEmailDomains,
    selectedEmailDomain,
    emailLocalPart,
    emailInputElement,
    emailAutofillElement,
    handleEmailAutofill,
    targetLabel,
    emailCredentialLabel,
    emailCredentialPlaceholder,
    resetURLPreview,
    selectedDNSProvider,
    dnsVerificationValid,
    formatVerificationExpiry,
    formatVerificationStageStatus,
    applyEmailDomain,
    toggleNotificationAdvanced,
    handleEmailInput,
    synchronizeEmailField,
    handleEmailPaste,
    handleEmailBlur,
    testDNS,
    handleSubmit,
    previousStep,
  };
}
