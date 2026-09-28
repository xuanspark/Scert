import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { settingsPayload } from "./SettingsPayload.js";
import { deploymentSummary, serverAuthLabel } from "./DeploymentFeedback.js";
import { useDomainForm } from "./DomainForm.js";

const defaultDNSVerificationTimeoutSeconds = 60;
const aliyunDNSVerificationTimeoutSeconds = 6;

function dnsVerificationTimeoutFor(providerID) {
  return String(providerID || "").trim().toLowerCase() === "aliyun"
    ? aliyunDNSVerificationTimeoutSeconds
    : defaultDNSVerificationTimeoutSeconds;
}

export function useDashboard() {
  const status = ref(null);
  const currentUser = ref(null);
  const domains = ref([]);
  const servers = ref([]);
  const sshKeys = ref([]);
  const certificates = ref([]);
  const tasks = ref([]);
  const auditLogs = ref([]);
  const settings = ref(null);
  const dnsProviderCatalog = ref([]);
  const domainServers = ref({});
  const domainPage = ref(1);
  const domainPageSize = ref(5);
  const domainPageSizeOptions = [5, 10, 15, 30];
  const loading = ref(true);
  const error = ref("");
  const notice = ref("");
  const domainForm = useDomainForm({ fetchAdmin, domains, servers, domainServers, notice, loadData });
  const { saving, formError, showForm, editingId, isEditing, formTitle, form,
    openCreate, openEdit, closeForm, saveDomain } = domainForm;
  const showServerForm = ref(false);
  const serverEditingId = ref("");
  const serverSaving = ref(false);
  const serverFormError = ref("");
  const showSSHKeyForm = ref(false);
  const showEmbeddedSSHKeyGuide = ref(false);
  const sshKeySaving = ref(false);
  const sshKeyFormError = ref("");
  const sshKeyPlatform = ref("windows");
  const sshKeyPassphraseVisible = ref(false);
  const sshKeyRandomSuffix = ref(createSSHKeyRandomSuffix());
  const showSettings = ref(false);
  const settingsSaving = ref(false);
  const settingsError = ref("");
  const dnsTesting = ref(false);
  const dnsTestError = ref("");
  const dnsVerificationElapsedSeconds = ref(0);
  const dnsVerificationTimeoutSeconds = ref(defaultDNSVerificationTimeoutSeconds);
  const verificationClock = ref(Date.now());
  let verificationTimer;
  let dnsVerificationTimer;
  let dnsRequestVersion = 0;
  const settingsForm = ref(createEmptySettingsForm());
  const showCertificateImport = ref(false);
  const importDomain = ref(null);
  const importSaving = ref(false);
  const importError = ref("");
  const importResult = ref(null);
  const importFiles = ref({
    certificate: null,
    private_key: null,
    issuer_bundle: null,
  });
  const showTaskDetails = ref(false);
  const taskDetails = ref(null);
  const taskAttempts = ref([]);
  const taskDetailsLoading = ref(false);
  const taskDetailsError = ref("");
  const deploymentResult = ref(null);
  const domainActionRunning = ref(false);
  const hasOpenModal = computed(
    () =>
      showForm.value ||
      showServerForm.value ||
      showSSHKeyForm.value ||
      showSettings.value ||
      showCertificateImport.value ||
      deploymentResult.value !== null ||
      showTaskDetails.value,
  );
  let previousBodyOverflow = "";

  const domainPageCount = computed(() =>
    Math.max(1, Math.ceil(domains.value.length / domainPageSize.value)),
  );
  const paginatedDomains = computed(() => {
    const start = (domainPage.value - 1) * domainPageSize.value;
    return domains.value.slice(start, start + domainPageSize.value);
  });
  const domainPageStart = computed(() =>
    domains.value.length === 0 ? 0 : (domainPage.value - 1) * domainPageSize.value + 1,
  );
  const domainPageEnd = computed(() =>
    Math.min(domainPage.value * domainPageSize.value, domains.value.length),
  );

  watch(domainPageSize, () => {
    domainPage.value = 1;
  });

  watch(domains, () => {
    if (domainPage.value > domainPageCount.value) {
      domainPage.value = domainPageCount.value;
    }
  });

  watch(hasOpenModal, (isOpen) => {
    if (isOpen) {
      previousBodyOverflow = document.body.style.overflow;
      document.body.style.overflow = "hidden";
      return;
    }
    document.body.style.overflow = previousBodyOverflow;
  });

  const sshKeyFileName = computed(
    () =>
      `${sanitizeSSHKeySegment(sshKeyCommandUsername.value, "用户名")}+scert-${sshKeyRandomSuffix.value}`,
  );
  const sshKeyPath = computed(() => `/ssl/ssh/${sshKeyFileName.value}`);
  const sshKeyPrivatePath = computed(() => `${sshKeyPath.value}.key`);
  const sshKeyPublicPath = computed(() => sshKeyPath.value);
  const sshKeyRemoteTarget = computed(
    () =>
      `${sanitizeSSHKeySegment(sshKeyCommandUsername.value, "用户名")}@${sanitizeSSHHost(sshKeyCommandHost.value)}`,
  );
  const sshKeyCommands = computed(() => ({
    windows:
      `New-Item -ItemType Directory -Force -Path "/ssl/ssh" | Out-Null; ssh-keygen -t ed25519 -C "scert-deploy" -f "${sshKeyPrivatePath.value}"; Move-Item -Force "${sshKeyPrivatePath.value}.pub" "${sshKeyPublicPath.value}"`,
    linux:
      `sudo install -d -m 700 -o "$(id -un)" -g "$(id -gn)" /ssl/ssh && ssh-keygen -t ed25519 -C "scert-deploy" -f "${sshKeyPrivatePath.value}" && mv -f "${sshKeyPrivatePath.value}.pub" "${sshKeyPublicPath.value}" && chmod 644 "${sshKeyPublicPath.value}"`,
  }));
  const sshPublicKeyCommands = computed(() => ({
    windows:
      `Get-Content "${sshKeyPublicPath.value}" | ssh ${sshKeyRemoteTarget.value} "umask 077; mkdir -p ~/.ssh; cat >> ~/.ssh/authorized_keys"`,
    linux:
      `ssh-copy-id -i "${sshKeyPublicPath.value}" ${sshKeyRemoteTarget.value}`,
  }));

  const certificateTypeLabels = {
    single: "单域名",
    wildcard: "泛域名",
    san: "SAN 多域名",
  };
  const taskStatusLabels = {
    queued: "排队中",
    running: "执行中",
    success: "已完成",
    failed: "失败",
    cancelled: "已取消",
  };
  taskStatusLabels.error = taskStatusLabels.failed;
  const taskTypeLabels = {
    management_renewal: "管理证书续期",
    certificate_issue: "申请证书",
    certificate_import: "导入证书",
    certificate_deploy: "部署证书",
    certificate_validate: "校验证书",
    certificate_restore: "恢复证书版本",
    renewal_run: "自动续期",
    dns_test: "DNS 在线测试",
  };
  const auditActionLabels = {
    management_renewal: "管理证书续期",
    certificate_issue: "申请证书",
    certificate_import: "导入证书",
    certificate_deploy: "部署证书",
    certificate_validate: "校验证书",
    certificate_version_restore: "恢复证书版本",
    task_cancel: "取消任务",
    settings_update: "更新系统设置",
    domain_create: "创建域名",
    domain_update: "更新域名",
    domain_delete: "删除域名",
    server_create: "创建服务器",
    server_update: "更新服务器",
    server_delete: "删除服务器",
    ssh_key_create: "添加 SSH 私钥",
    ssh_key_delete: "删除 SSH 私钥",
    renewal_run: "执行自动续期",
  };
  const taskStageLabels = {
    management_renewal: "管理证书续期",
    preflight: "续期前置检查",
    issue: "申请管理证书",
    activate: "激活管理证书",
    record: "保存任务记录",
    retry_wait: "等待续期重试",
    service_restart: "服务重启恢复",
    queued: "排队中",
    certificate_issue: "申请证书",
    certificate_validate: "校验证书",
    certificate_deploy: "部署证书",
    deployment: "远程部署",
    verification: "部署后校验",
    rollback: "回滚处理",
    completed: "已完成",
  };
  const attemptStatusLabels = {
    running: "执行中",
    success: "已完成",
    failed: "失败",
    cancelled: "已取消",
  };
  const domainStatusLabels = {
    pending: "等待申请",
    active: "正常",
    valid: "正常",
    error: "错误",
  };
  const httpsModeLabels = { direct: "核心服务 HTTPS", proxy: "反向代理 HTTPS" };
  const environmentLabels = { staging: "测试环境", production: "生产环境" };
  const webServerLabels = {
    nginx: "Nginx",
    caddy: "Caddy",
    apache: "Apache",
    custom: "自定义网站服务",
  };
  const dnsProviders = [
    { value: "cloudflare", label: "Cloudflare" },
    { value: "aliyun", label: "阿里云 DNS" },
    { value: "dnspod", label: "DNSPod" },
    { value: "cname", label: "CNAME 委派" },
    { value: "huaweicloud", label: "华为云" },
    { value: "tencentcloud", label: "腾讯云" },
    { value: "route53", label: "AWS Route 53" },
    { value: "googlecloud", label: "Google Cloud DNS" },
    { value: "other", label: "其他服务商" },
  ];

  const selectedDNSProvider = computed(() =>
    dnsProviderCatalog.value.find(
      (provider) => provider.id === settingsForm.value.dns.provider,
    ) || null,
  );
  const dnsVerificationRequired = computed(() => {
    const current = settingsForm.value.dns;
    const saved = settings.value?.dns;
    if (!current.provider) return false;
    return (
      current.provider !== saved?.provider ||
      !saved?.credential_configured ||
      Boolean(current.test_domain.trim()) ||
      Object.values(current.credentials || {}).some((value) => String(value || "").trim()) ||
      Boolean(current.verification_token) ||
      current.verification_state === "pending"
    );
  });
  const dnsReadyToSave = computed(() => {
    verificationClock.value;
    if (dnsTesting.value) return false;
    if (!dnsVerificationRequired.value) return true;
    const dns = settingsForm.value.dns;
    return Boolean(dns.credential_ref && dns.verification_token) &&
      new Date(dns.expires_at).getTime() > Date.now();
  });

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

  watch(
    () => [settingsForm.value.dns.provider, settingsForm.value.dns.test_domain, settingsForm.value.dns.credentials],
    handleDNSProviderChange,
    { deep: true, flush: "sync" },
  );

  const defaultCertificatePath = "/ssl/{域名}/{证书类型}/cert.pem";
  const defaultCertificateChainPath = "/ssl/{域名}/{证书类型}/fullchain.pem";
  const defaultPrivateKeyPath = "/ssl/{域名}/{证书类型}/key.pem";

  const serverForm = ref(createEmptyServerForm());
  const sshKeyForm = ref(createEmptySSHKeyForm());

  const sshKeyCommandUsername = computed(
    () => String(serverForm.value.username || "").trim() || "用户名",
  );
  const sshKeyCommandHost = computed(
    () => String(serverForm.value.address || "").trim() || "服务器地址",
  );

  watch(sshKeyFileName, (fileName, previousFileName) => {
    if (!showEmbeddedSSHKeyGuide.value) return;
    const currentName = String(sshKeyForm.value.name || "").trim();
    if (!currentName || currentName === previousFileName) {
      sshKeyForm.value.name = fileName;
    }
  });
  async function fetchAdmin(path, options = {}) {
    const response = await fetch(path, {
      ...options,
      credentials: "include",
    });
    if (response.status === 401) {
      window.location.assign("/login");
      throw new Error("登录会话已过期，请重新登录");
    }
    return response;
  }

  async function responseError(response, fallback) {
    const result = await response.json().catch(() => ({}));
    if (result.error) return result.error;
    if (response.status === 403) return "当前账号没有管理权限";
    if (response.status === 409) return "当前服务状态不允许执行此操作";
    if (response.status >= 500) return "核心服务暂时不可用，请稍后重试";
    return fallback;
  }

  function taskTypeLabel(value) {
    return displayLabel(taskTypeLabels, value);
  }

  function auditActionLabel(value) {
    if (typeof value === "string" && /\p{Script=Han}/u.test(value)) {
      return value.trim();
    }
    return displayLabel(auditActionLabels, value);
  }

  function taskStageLabel(value) {
    return displayLabel(taskStageLabels, value);
  }

  function attemptStatusLabel(value) {
    return displayLabel(attemptStatusLabels, value);
  }

  function createEmptyServerForm() {
    return {
      name: "",
      address: "",
      port: 22,
      username: "",
      auth_method: "password",
      private_key_ref: "",
      password: "",
      passwordConfigured: false,
      host_key_fingerprint: "",
      certificate_path: defaultCertificatePath,
      private_key_path: defaultPrivateKeyPath,
      certificate_chain_path: defaultCertificateChainPath,
      web_server: "nginx",
      validation_command: "",
      reload_command: "",
      server_ids: [],
    };
  }

  function createEmptySSHKeyForm() {
    return { name: "", private_key: "", passphrase: "" };
  }

  function createSSHKeyRandomSuffix() {
    const bytes = new Uint8Array(6);
    crypto.getRandomValues(bytes);
    return Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join(
      "",
    );
  }

  function sanitizeSSHKeySegment(value, fallback) {
    const normalized = String(value || "")
      .trim()
      .replace(/[^\p{L}\p{N}._-]+/gu, "-")
      .replace(/^-+|-+$/g, "");
    return normalized || fallback;
  }

  function sanitizeSSHHost(value) {
    const normalized = String(value || "")
      .trim()
      .replace(/[^a-zA-Z0-9.:[\]-]+/g, "");
    return normalized || "服务器地址";
  }

  function createEmptySettingsForm() {
    return {
      server: { host: "", port: 5200, https_mode: "direct", proxy_https_port: 0 },
      web: { enabled: true, hostname: "", public_ip: "" },
      paths: { certificate: "" },
      acme: {
        ca: "letsencrypt",
        environment: "staging",
        account_email: "",
        renewal_check_interval_days: 3,
        renewal_near_expiry_days: 10,
        renewal_threshold_days: 6,
        renewal_urgent_threshold_days: 3,
        renewal_max_retries: 3,
        renewal_retry_interval_mins: 30,
        propagation_timeout_secs: 300,
        propagation_polling_interval_secs: 5,
      },
      dns: {
        provider: "",
        credential_configured: false,
        verification_state: "not_configured",
        test_domain: "",
        credentials: {},
        credential_ref: "",
        verification_token: "",
        expires_at: "",
        test_report: null,
      },
      notification: {
        http_enabled: false,
        smtp_enabled: false,
        provider: "",
        endpoint: "",
        http_path: "/api/email/send",
        http_login_path: "/api/login",
        http_auth_mode: "none",
        event_mode: "detailed",
        include_deployment_details: true,
        include_certificate_serial: false,
        include_management_url: false,
        sender_name: "Scert",
        sender_email: "",
        recipients: [],
        timeout_secs: 10,
        retry_count: 3,
        retry_intervals_secs: [30, 120, 600],
        smtp_host: "",
        smtp_port: 587,
        smtp_username: "",
        smtp_password: "",
        smtp_mode: "starttls",
        smtp_allow_plaintext_auth: false,
        smtp_credential_configured: false,
      },
    };
  }

  function normalizeSettings(value) {
    const defaults = createEmptySettingsForm();
    const source = value || {};
    return {
      ...defaults,
      ...source,
      server: { ...defaults.server, ...(source.server || {}) },
      web: { ...defaults.web, ...(source.web || {}) },
      paths: { ...defaults.paths, ...(source.paths || {}) },
      acme: { ...defaults.acme, ...(source.acme || {}) },
      dns: {
        ...defaults.dns,
        ...(source.dns || {}),
        credentials: {},
        credential_ref: "",
        verification_token: "",
        expires_at: "",
        test_domain: "",
        test_report: null,
      },
      notification: {
        ...defaults.notification,
        ...(source.notification || {}),
        smtp_password: "",
        recipients: Array.isArray(source.notification?.recipients)
          ? source.notification.recipients
          : [],
      },
    };
  }

  function displayLabel(labels, value) {
    return labels[value] || (value ? "未知" : "—");
  }

  function serverSSHKey(server) {
    if (!server?.private_key_ref) return null;
    return sshKeys.value.find((item) => item.reference === server.private_key_ref) || null;
  }

  function serverSSHKeyStatusLabel(server) {
    const labels = {
      not_configured: "未配置",
      pending: "部署中",
      deployed: "已部署并验证",
      manual: "已配置，待验证",
      failed: "部署失败，保留密码回退",
    };
    return labels[server?.ssh_key_status] || "未配置";
  }

  function serverSSHKeyStatusClass(server) {
    if (server?.ssh_key_status === "deployed") return "success";
    if (server?.ssh_key_status === "failed") return "danger";
    if (server?.ssh_key_status === "pending") return "warning";
    return "neutral";
  }

  function serverSSHCommand(server) {
    if (!server?.address || !server?.username) return "—";
    const port = Number(server.port || 22);
    const portOption = port === 22 ? "" : ` -p ${port}`;
    return `ssh${portOption} ${server.username}@${server.address}`;
  }

  function serverSSHKeyHint(server) {
    if (server?.ssh_key_status === "deployed") {
      return "Scert 已自动部署公钥并验证密钥登录，后续部署默认使用托管私钥。";
    }
    if (server?.ssh_key_status === "failed") {
      return server.ssh_key_error || "公钥部署或密钥登录验证失败，当前仍使用密码回退。";
    }
    if (server?.auth_method === "private_key") {
      return "当前使用已配置的私钥连接，尚未完成自动部署验证。";
    }
    return "保存服务器后，Scert 会先用密码部署公钥，再自动验证密钥登录。";
  }

  function formatDate(value) {
    if (!value) return "尚未签发";
    return new Intl.DateTimeFormat("zh-CN", {
      dateStyle: "medium",
      timeZone: "Asia/Shanghai",
    }).format(new Date(value));
  }

  function daysRemaining(value) {
    if (!value) return "待申请";
    const days = Math.ceil((new Date(value).getTime() - Date.now()) / 86400000);
    return days >= 0 ? `剩余 ${days} 天` : `已过期 ${Math.abs(days)} 天`;
  }

  function statusClass(value) {
    return value === "active" || value === "valid" || value === "success"
      ? "success"
      : value === "error" || value === "failed"
        ? "danger"
        : "warning";
  }

  function previousDomainPage() {
    domainPage.value = Math.max(1, domainPage.value - 1);
  }

  function nextDomainPage() {
    domainPage.value = Math.min(domainPageCount.value, domainPage.value + 1);
  }

  function openServerCreate() {
    serverEditingId.value = "";
    serverForm.value = createEmptyServerForm();
    serverFormError.value = "";
    showServerForm.value = true;
  }

  function openServerEdit(item) {
    serverEditingId.value = item.id;
    serverForm.value = {
      ...item,
      passwordConfigured: item.password_configured === true,
      password: "",
      validation_command: (item.validation_command || []).join("\n"),
      reload_command: (item.reload_command || []).join("\n"),
      server_ids: [],
    };
    serverFormError.value = "";
    showServerForm.value = true;
  }

  function closeServerForm() {
    if (!serverSaving.value) {
      showEmbeddedSSHKeyGuide.value = false;
      showServerForm.value = false;
    }
  }

  function openSSHKeyCreate(options = {}) {
    sshKeyForm.value = createEmptySSHKeyForm();
    sshKeyFormError.value = "";
    sshKeyPlatform.value = "windows";
    sshKeyPassphraseVisible.value = false;
    sshKeyRandomSuffix.value = createSSHKeyRandomSuffix();
    sshKeyForm.value.name = sshKeyFileName.value;
    showEmbeddedSSHKeyGuide.value = options.embedded === true;
    showSSHKeyForm.value = options.embedded !== true;
  }

  function closeSSHKeyForm() {
    if (sshKeySaving.value) return;
    sshKeyForm.value = createEmptySSHKeyForm();
    sshKeyFormError.value = "";
    sshKeyPassphraseVisible.value = false;
    showEmbeddedSSHKeyGuide.value = false;
    showSSHKeyForm.value = false;
  }

  async function importSSHKeyFile(event) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    try {
      const content = await file.text();
      if (!content.trim()) throw new Error("选择的私钥文件为空");
      sshKeyForm.value.private_key = content;
      sshKeyFormError.value = "";
      notice.value = `已读取私钥文件：${file.name}`;
    } catch (err) {
      sshKeyFormError.value = err.message || "读取私钥文件失败";
    }
  }

  async function copySSHKeyCommand(value, message) {
    try {
      await navigator.clipboard.writeText(value);
      notice.value = message;
    } catch {
      sshKeyFormError.value = "浏览器未允许访问剪贴板，请手动复制命令";
    }
  }

  async function saveSSHKey() {
    sshKeyFormError.value = "";
    sshKeySaving.value = true;
    try {
      const response = await fetchAdmin("/api/v1/ssh-keys", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(sshKeyForm.value),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "保存 SSH 私钥失败");
      if (showEmbeddedSSHKeyGuide.value && result.reference) {
        serverForm.value.private_key_ref = result.reference;
      }
      sshKeyForm.value = createEmptySSHKeyForm();
      showEmbeddedSSHKeyGuide.value = false;
      showSSHKeyForm.value = false;
      notice.value = "SSH 私钥已加密保存";
      await loadData();
    } catch (err) {
      sshKeyFormError.value = err.message || "保存 SSH 私钥失败";
    } finally {
      sshKeySaving.value = false;
    }
  }

  async function deleteSSHKey(item) {
    if (
      !window.confirm(
        `确定删除 SSH 私钥“${item.name}”吗？正在被服务器使用的私钥无法删除。`,
      )
    )
      return;
    try {
      const response = await fetchAdmin(`/api/v1/ssh-keys/${item.id}`, {
        method: "DELETE",
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "删除 SSH 私钥失败");
      notice.value = "SSH 私钥已删除";
      await loadData();
    } catch (err) {
      error.value = err.message || "删除 SSH 私钥失败";
    }
  }

  function parseCommand(value) {
    return value
      .split(/\n/)
      .map((item) => item.trim())
      .filter(Boolean);
  }

  async function saveServer() {
    serverFormError.value = "";
    serverSaving.value = true;
    try {
      const payload = {
        name: serverForm.value.name,
        address: serverForm.value.address,
        port: Number(serverForm.value.port),
        username: serverForm.value.username,
        auth_method: serverForm.value.auth_method,
        private_key_ref: serverForm.value.private_key_ref,
        host_key_fingerprint: serverForm.value.host_key_fingerprint,
        certificate_path: serverForm.value.certificate_path,
        private_key_path: serverForm.value.private_key_path,
        certificate_chain_path: serverForm.value.certificate_chain_path,
        web_server: serverForm.value.web_server,
        validation_command: parseCommand(serverForm.value.validation_command),
        reload_command: parseCommand(serverForm.value.reload_command),
      };
      if (serverForm.value.auth_method === "password") {
        payload.password = serverForm.value.password;
      }
      const path = serverEditingId.value
        ? `/api/v1/servers/${serverEditingId.value}`
        : "/api/v1/servers";
      const response = await fetchAdmin(path, {
        method: serverEditingId.value ? "PUT" : "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "保存服务器失败");
      showServerForm.value = false;
      notice.value = serverEditingId.value
        ? "服务器配置已更新"
        : "服务器已添加";
      await loadData();
    } catch (err) {
      serverFormError.value = err.message || "保存服务器失败";
    } finally {
      serverSaving.value = false;
    }
  }

  async function runDomainAction(item, action, successMessage) {
    if (domainActionRunning.value) return;
    domainActionRunning.value = true;
    if (action === "deploy") {
      deploymentResult.value = { domain: item.name, running: true, report: null, error: "" };
    }
    error.value = "";
    notice.value = "";
    try {
      const options = { method: "POST" };
      if (action === "issue") {
        const suffix = crypto.randomUUID ? crypto.randomUUID() : createSSHKeyRandomSuffix();
        options.headers = { "Idempotency-Key": `issue-${item.id}-${suffix}` };
      }
      const response = await fetchAdmin(`/api/v1/domains/${item.id}/${action}`, options);
      const result = await response.json().catch(() => ({}));
      if (action === "deploy") {
        deploymentResult.value = {
          domain: item.name,
          running: false,
          report: result.deployment || null,
          error: response.ok && result.deployment ? "" : result.error || deploymentSummary(null),
        };
      }
      if (!response.ok) throw new Error(result.error || "域名操作失败");
      notice.value = successMessage;
      if (action === "deploy") notice.value = deploymentSummary(result.deployment);
    } catch (err) {
      error.value = err.message || "域名操作失败";
    } finally {
      const actionError = error.value;
      if (action === "deploy" && deploymentResult.value) {
        deploymentResult.value.running = false;
        if (actionError) deploymentResult.value.error = actionError;
      }
      domainActionRunning.value = false;
      await loadData();
      if (actionError) error.value = actionError;
    }
  }
  function issueDomain(item) {
    return runDomainAction(item, "issue", `已提交 ${item.name} 的证书申请`);
  }

  function validateDomain(item) {
    return runDomainAction(item, "certificate/validate", `已提交 ${item.name} 的证书校验`);
  }

  function deployDomain(item) {
    return runDomainAction(item, "deploy", `已提交 ${item.name} 的证书部署`);
  }

  async function openTaskDetails(item) {
    showTaskDetails.value = true;
    taskDetails.value = null;
    taskAttempts.value = [];
    taskDetailsError.value = "";
    taskDetailsLoading.value = true;
    try {
      const [taskResponse, attemptsResponse] = await Promise.all([
        fetchAdmin(`/api/v1/tasks/${item.id}`),
        fetchAdmin(`/api/v1/tasks/${item.id}/attempts`),
      ]);
      if (!taskResponse.ok) {
        throw new Error(await responseError(taskResponse, "读取任务详情失败"));
      }
      if (!attemptsResponse.ok) {
        throw new Error(await responseError(attemptsResponse, "读取任务尝试记录失败"));
      }
      taskDetails.value = await taskResponse.json();
      taskAttempts.value = (await attemptsResponse.json()).items || [];
    } catch (err) {
      taskDetailsError.value = err.message || "读取任务详情失败";
    } finally {
      taskDetailsLoading.value = false;
    }
  }

  function closeTaskDetails() {
    showTaskDetails.value = false;
    taskDetails.value = null;
    taskAttempts.value = [];
    taskDetailsError.value = "";
  }

  async function cancelTask(item) {
    if (!window.confirm("确定取消这项正在执行的证书任务吗？")) return;
    error.value = "";
    notice.value = "";
    try {
      const response = await fetchAdmin(`/api/v1/tasks/${item.id}`, { method: "DELETE" });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "取消任务失败");
      notice.value = "证书任务已取消";
      await loadData();
    } catch (err) {
      error.value = err.message || "取消任务失败";
    }
  }
  async function runRenewalNow() {
    error.value = "";
    notice.value = "";
    try {
      const response = await fetchAdmin("/api/v1/renewal/run", { method: "POST" });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "执行续期检查失败");
      const report = result.report || {};
      notice.value = `续期检查完成：检查 ${report.checked || 0} 项，选中 ${report.selected || 0} 项，成功 ${report.succeeded || 0} 项，失败 ${report.failed || 0} 项`;
      await loadData();
    } catch (err) {
      error.value = err.message || "执行续期检查失败";
    }
  }

  function handleDNSProviderChange() {
    dnsRequestVersion += 1;
    stopDNSVerificationTimer();
    dnsTesting.value = false;
    const dns = settingsForm.value.dns;
    const saved = settings.value?.dns;
    dnsTestError.value = "";
    dns.credential_ref = "";
    dns.verification_token = "";
    dns.expires_at = "";
    dns.test_report = null;
    const hasNewInput = Boolean(dns.test_domain.trim()) ||
      Object.values(dns.credentials || {}).some((value) => String(value || "").trim());
    if (!hasNewInput && dns.provider && dns.provider === saved?.provider && saved.credential_configured) {
      dns.verification_state = saved.verification_state || "verified";
      return;
    }
    dns.verification_state = dns.provider ? "pending" : "not_configured";
  }

  function clearDNSCredentials() {
    settingsForm.value.dns.credentials = {};
    dnsTestError.value = "";
  }

  async function testDNS() {
    handleDNSProviderChange();
    const dns = settingsForm.value.dns;
    const provider = selectedDNSProvider.value;
    dnsTestError.value = "";
    dns.test_report = null;
    if (!dns.provider || !provider) {
      dnsTestError.value = "请选择 DNS 验证服务商";
      return;
    }
    if (!dns.test_domain.trim()) {
      dnsTestError.value = "请输入 DNS 在线测试域名";
      return;
    }
    const credentials = Object.fromEntries(
      Object.entries(dns.credentials || {})
        .map(([key, value]) => [key, String(value || "").trim()])
        .filter(([, value]) => value),
    );
    const missingField = (provider.credential_fields || []).find(
      (field) => field.required && !credentials[field.name],
    );
    if (missingField) {
      dnsTestError.value = `请填写${missingField.label}`;
      return;
    }
    const requestVersion = dnsRequestVersion;
    dnsTesting.value = true;
    dns.verification_state = "pending";
    startDNSVerificationTimer(requestVersion, dns.provider);
    try {
      const response = await fetchAdmin("/api/v1/dns/test", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          provider: dns.provider,
          test_domain: dns.test_domain.trim(),
          credentials,
        }),
      });
      const result = await response.json().catch(() => ({}));
      if (requestVersion !== dnsRequestVersion) return;
      updateDNSVerificationTimeoutSeconds(result.verification_timeout_secs);
      dns.test_report = result.test_report || null;
      if (!response.ok) {
        throw new Error(result.error || "DNS API 在线测试失败");
      }
      dns.credential_ref = result.credential_ref || "";
      dns.verification_token = result.verification_token || "";
      dns.expires_at = result.expires_at || "";
      dns.verification_state = "verified";
      dns.credential_configured = true;
      notice.value = "DNS API 在线测试通过，可以保存系统设置";
    } catch (err) {
      if (requestVersion !== dnsRequestVersion) return;
      dns.credential_ref = "";
      dns.verification_token = "";
      dns.verification_state = "pending";
      dnsTestError.value = err.message || "DNS API 在线测试失败";
    } finally {
      if (requestVersion === dnsRequestVersion) {
        stopDNSVerificationTimer(requestVersion);
        dnsTesting.value = false;
      }
    }
  }

  function openSettings() {
    settingsError.value = "";
    dnsTestError.value = "";
    settingsForm.value = normalizeSettings(settings.value);
    showSettings.value = true;
  }

  function closeSettings() {
    if (!settingsSaving.value) {
      handleDNSProviderChange();
      showSettings.value = false;
    }
  }

  async function saveSettings() {
    settingsError.value = "";
    verificationClock.value = Date.now();
    if (!dnsReadyToSave.value) {
      settingsError.value = "DNS 服务商配置尚未通过在线测试，请先完成测试";
      return;
    }
    settingsSaving.value = true;
    try {
      const payload = settingsPayload(settingsForm.value);
      const response = await fetchAdmin("/api/v1/settings", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "保存系统设置失败");
      settings.value = result.settings;
      showSettings.value = false;
      notice.value = result.message || "系统设置已保存";
      await loadData();
    } catch (err) {
      settingsError.value = err.message || "保存系统设置失败";
    } finally {
      settingsSaving.value = false;
    }
  }

  function openCertificateImport(item) {
    importDomain.value = item;
    importFiles.value = {
      certificate: null,
      private_key: null,
      issuer_bundle: null,
    };
    importResult.value = null;
    importError.value = "";
    showCertificateImport.value = true;
  }

  function closeCertificateImport() {
    if (!importSaving.value) showCertificateImport.value = false;
  }

  function selectCertificateImportFile(kind, event) {
    importFiles.value[kind] = event.target.files?.[0] || null;
    importError.value = "";
  }

  async function importCertificate() {
    if (importSaving.value || importResult.value) return;
    importError.value = "";
    importResult.value = null;
    if (!importFiles.value.certificate || !importFiles.value.private_key) {
      importError.value = "请选择证书文件和私钥文件";
      return;
    }
    const maxSize = 20 * 1024 * 1024;
    const files = Object.values(importFiles.value).filter(Boolean);
    if (files.some((file) => file.size > maxSize)) {
      importError.value = "单个证书文件不能超过 20 MB";
      return;
    }
    importSaving.value = true;
    try {
      const body = new FormData();
      body.append("certificate", importFiles.value.certificate);
      body.append("private_key", importFiles.value.private_key);
      if (importFiles.value.issuer_bundle) {
        body.append("issuer_bundle", importFiles.value.issuer_bundle);
      }
      const response = await fetchAdmin(
        `/api/v1/domains/${importDomain.value.id}/certificate/import`,
        { method: "POST", body },
      );
      const result = await response.json().catch(() => ({}));
      if (!response.ok && result.imported !== true) {
        throw new Error(result.error || "导入证书失败");
      }
      importResult.value = result;
      notice.value = `域名 ${importDomain.value.name} 的证书已导入`;
      if (!response.ok || result.deployment?.total > 0) {
        showCertificateImport.value = false;
        deploymentResult.value = {
          domain: importDomain.value.name,
          running: false,
          certificateImported: true,
          report: result.deployment || null,
          error: response.ok ? "" : result.error || "证书已导入，但部署未完成。请重试部署，无需重新导入。",
        };
      }
      await loadData();
    } catch (err) {
      importError.value = err.message || "导入证书失败";
    } finally {
      importSaving.value = false;
    }
  }

  async function deleteServer(item) {
    if (!window.confirm(`确定删除服务器“${item.name}”吗？关联域名也会解除。`))
      return;
    try {
      const response = await fetchAdmin(`/api/v1/servers/${item.id}`, {
        method: "DELETE",
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "删除服务器失败");
      notice.value = "服务器已删除";
      await loadData();
    } catch (err) {
      error.value = err.message || "删除服务器失败";
    }
  }

  async function deleteDomain(item) {
    if (
      !window.confirm(
        `确定删除域名“${item.name}”吗？删除前请确认没有正在执行的任务。`,
      )
    )
      return;
    error.value = "";
    notice.value = "";
    try {
      const response = await fetchAdmin(`/api/v1/domains/${item.id}`, {
        method: "DELETE",
      });
      if (!response.ok) {
        const result = await response.json().catch(() => ({}));
        throw new Error(result.error || "删除域名失败");
      }
      notice.value = "域名已删除";
      await loadData();
    } catch (err) {
      error.value = err.message || "删除域名失败";
    }
  }

  async function loadData() {
    loading.value = true;
    error.value = "";
    try {
      const [
        statusResponse,
        authResponse,
        domainResponse,
        serverResponse,
        sshKeyResponse,
        certificateResponse,
        taskResponse,
        auditResponse,
        settingsResponse,
        dnsProviderResponse,
      ] = await Promise.all([
        fetchAdmin("/api/v1/status"),
        fetchAdmin("/api/v1/auth/session"),
        fetchAdmin("/api/v1/domains"),
        fetchAdmin("/api/v1/servers"),
        fetchAdmin("/api/v1/ssh-keys"),
        fetchAdmin("/api/v1/certificates"),
        fetchAdmin("/api/v1/tasks?limit=20"),
        fetchAdmin("/api/v1/audit-logs?limit=20"),
        fetchAdmin("/api/v1/settings"),
        fetchAdmin("/api/v1/dns/providers"),
      ]);
      const failedResponse = [
        statusResponse,
        domainResponse,
        serverResponse,
        sshKeyResponse,
        certificateResponse,
        taskResponse,
        auditResponse,
        settingsResponse,
        dnsProviderResponse,
      ].find((response) => !response.ok);
      if (failedResponse) {
        throw new Error(await responseError(failedResponse, "读取管理数据失败"));
      }
      status.value = await statusResponse.json();
      currentUser.value = await authResponse.json();
      domains.value = (await domainResponse.json()).items || [];
      servers.value = (await serverResponse.json()).items || [];
      sshKeys.value = (await sshKeyResponse.json()).items || [];
      certificates.value = (await certificateResponse.json()).items || [];
      tasks.value = (await taskResponse.json()).items || [];
      auditLogs.value = (await auditResponse.json()).items || [];
      settings.value = await settingsResponse.json();
      const providerResult = await dnsProviderResponse.json();
      dnsProviderCatalog.value = (providerResult.items || []).map((item) => ({
        ...item,
        value: item.id,
        label: item.name,
      }));
      settingsForm.value = normalizeSettings(settings.value);
      const associations = await Promise.all(
        domains.value.map(async (item) => {
          const response = await fetchAdmin(
            `/api/v1/domains/${item.id}/servers`,
          );
          return [
            item.id,
            response.ok ? (await response.json()).items || [] : [],
          ];
        }),
      );
      domainServers.value = Object.fromEntries(associations);
    } catch (err) {
      error.value = err.message || "无法连接核心服务，请先启动 scert serve";
    } finally {
      loading.value = false;
    }
  }

  onMounted(() => {
    loadData();
    verificationTimer = window.setInterval(() => {
      verificationClock.value = Date.now();
    }, 1000);
  });
  onBeforeUnmount(() => {
    dnsRequestVersion += 1;
    stopDNSVerificationTimer();
    if (verificationTimer) window.clearInterval(verificationTimer);
    document.body.style.overflow = previousBodyOverflow;
  });

  return {
    ...domainForm,
    status,
    currentUser,
    domains,
    servers,
    sshKeys,
    certificates,
    tasks,
    auditLogs,
    settings,
    settingsForm,
    dnsProviderCatalog,
    selectedDNSProvider,
    dnsTesting,
    dnsTestError,
    dnsVerificationElapsedSeconds,
    dnsVerificationTimeoutSeconds,
    dnsReadyToSave,
    dnsVerificationRequired,
    handleDNSProviderChange,
    clearDNSCredentials,
    testDNS,
    domainServers,
    domainPage,
    domainPageSize,
    domainPageSizeOptions,
    domainPageCount,
    paginatedDomains,
    domainPageStart,
    domainPageEnd,
    previousDomainPage,
    nextDomainPage,
    loading,
    saving,
    error,
    notice,
    formError,
    showForm,
    editingId,
    showServerForm,
    serverEditingId,
    serverSaving,
    serverFormError,
    showSSHKeyForm,
    showEmbeddedSSHKeyGuide,
    sshKeySaving,
    sshKeyFormError,
    certificateTypeLabels,
    domainStatusLabels,
    taskStatusLabels,
    taskTypeLabels,
    auditActionLabels,
    taskStageLabels,
    attemptStatusLabels,
    httpsModeLabels,
    dnsProviders,
    environmentLabels,
    webServerLabels,
    serverAuthLabel,
    serverSSHKey,
    serverSSHKeyStatusLabel,
    serverSSHKeyStatusClass,
    serverSSHCommand,
    serverSSHKeyHint,
    isEditing,
    formTitle,
    form,
    serverForm,
    sshKeyForm,
    sshKeyPlatform,
    sshKeyPassphraseVisible,
    sshKeyCommandUsername,
    sshKeyCommandHost,
    sshKeyFileName,
    sshKeyPath,
    sshKeyPrivatePath,
    sshKeyPublicPath,
    showSettings,
    settingsSaving,
    settingsError,
    showCertificateImport,
    importDomain,
    importSaving,
    importError,
    importResult,
    sshKeyCommands,
    sshPublicKeyCommands,
    defaultCertificatePath,
    defaultCertificateChainPath,
    defaultPrivateKeyPath,
    openCreate,
    openEdit,
    closeForm,
    openServerCreate,
    openServerEdit,
    closeServerForm,
    openSSHKeyCreate,
    closeSSHKeyForm,
    importSSHKeyFile,
    copySSHKeyCommand,
    saveSSHKey,
    deleteSSHKey,
    openCertificateImport,
    closeCertificateImport,
    selectCertificateImportFile,
    importCertificate,
    saveServer,
    deleteServer,
    saveDomain,
    deleteDomain,
    issueDomain,
    validateDomain,
    deployDomain,
    cancelTask,
    openTaskDetails,
    closeTaskDetails,
    showTaskDetails,
    taskDetails,
    taskAttempts,
    taskDetailsLoading,
    taskDetailsError,
    deploymentResult,
    taskTypeLabel,
    auditActionLabel,
    taskStageLabel,
    attemptStatusLabel,
    runRenewalNow,
    openSettings,
    closeSettings,
    saveSettings,
    loadData,
    displayLabel,
    formatDate,
    daysRemaining,
    statusClass,
  };
}
