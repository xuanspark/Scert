import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

export function useDashboard() {
  const scertLogo = "/Scert.png";

  const status = ref(null);
  const domains = ref([]);
  const servers = ref([]);
  const sshKeys = ref([]);
  const certificates = ref([]);
  const tasks = ref([]);
  const auditLogs = ref([]);
  const settings = ref(null);
  const domainServers = ref({});
  const loading = ref(true);
  const saving = ref(false);
  const error = ref("");
  const notice = ref("");
  const formError = ref("");
  const showForm = ref(false);
  const editingId = ref("");
  const showServerForm = ref(false);
  const serverEditingId = ref("");
  const serverSaving = ref(false);
  const serverFormError = ref("");
  const showSSHKeyForm = ref(false);
  const sshKeySaving = ref(false);
  const sshKeyFormError = ref("");
  const sshKeyPlatform = ref("windows");
  const sshKeyPassphraseVisible = ref(false);
  const sshKeyCommandUsername = ref("用户名");
  const sshKeyCommandHost = ref("服务器地址");
  const sshKeyRandomSuffix = ref(createSSHKeyRandomSuffix());
  const showSettings = ref(false);
  const settingsSaving = ref(false);
  const settingsError = ref("");
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
  const hasOpenModal = computed(
    () =>
      showForm.value ||
      showServerForm.value ||
      showSSHKeyForm.value ||
      showSettings.value,
  );
  let previousBodyOverflow = "";

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
  const domainStatusLabels = {
    pending: "等待申请",
    active: "正常",
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

  const isEditing = computed(() => Boolean(editingId.value));
  const formTitle = computed(() =>
    isEditing.value ? "编辑域名配置" : "添加托管域名",
  );

  const defaultCertificatePath = "/ssl/{域名}/{证书类型}/cert.pem";
  const defaultCertificateChainPath = "/ssl/{域名}/{证书类型}/fullchain.pem";
  const defaultPrivateKeyPath = "/ssl/{域名}/{证书类型}/key.pem";

  const form = ref(createEmptyForm());
  const serverForm = ref(createEmptyServerForm());
  const sshKeyForm = ref(createEmptySSHKeyForm());

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

  function createEmptyForm() {
    return {
      name: "",
      certificate_type: "single",
      sans: "",
      auto_renew: true,
      acme_environment: "staging",
      dns_provider: "",
      server_ids: [],
    };
  }

  function createEmptyServerForm() {
    return {
      name: "",
      address: "",
      port: 22,
      username: "",
      private_key_ref: "",
      host_key_fingerprint: "",
      certificate_path: defaultCertificatePath,
      private_key_path: defaultPrivateKeyPath,
      certificate_chain_path: defaultCertificateChainPath,
      web_server: "nginx",
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
      server: { host: "", port: 5200, https_mode: "direct" },
      web: { enabled: true, hostname: "", public_ip: "" },
      paths: { certificate: "" },
      acme: {
        ca: "letsencrypt",
        environment: "staging",
        account_email: "",
        renewal_threshold_days: 6,
        propagation_timeout_secs: 900,
        propagation_polling_interval_secs: 10,
      },
      dns: { provider: "" },
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
      dns: { ...defaults.dns, ...(source.dns || {}) },
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
    return value === "active"
      ? "success"
      : value === "error"
        ? "danger"
        : "warning";
  }

  function openCreate() {
    editingId.value = "";
    form.value = createEmptyForm();
    formError.value = "";
    showForm.value = true;
  }

  function openEdit(item) {
    editingId.value = item.id;
    form.value = {
      name: item.name,
      certificate_type: item.certificate_type,
      sans: (item.sans || []).filter((name) => name !== item.name).join("\n"),
      auto_renew: item.auto_renew !== false,
      acme_environment: item.acme_environment || "staging",
      dns_provider: item.dns_provider || "",
      server_ids: (domainServers.value[item.id] || []).map(
        (server) => server.id,
      ),
    };
    formError.value = "";
    showForm.value = true;
  }

  function closeForm() {
    if (!saving.value) showForm.value = false;
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
      reload_command: (item.reload_command || []).join("\n"),
      server_ids: [],
    };
    serverFormError.value = "";
    showServerForm.value = true;
  }

  function closeServerForm() {
    if (!serverSaving.value) showServerForm.value = false;
  }

  function openSSHKeyCreate() {
    sshKeyForm.value = createEmptySSHKeyForm();
    sshKeyFormError.value = "";
    sshKeyPlatform.value = "windows";
    sshKeyPassphraseVisible.value = false;
    sshKeyCommandUsername.value = serverForm.value.username.trim() || "用户名";
    sshKeyCommandHost.value = serverForm.value.address.trim() || "服务器地址";
    sshKeyRandomSuffix.value = createSSHKeyRandomSuffix();
    sshKeyForm.value.name = sshKeyFileName.value;
    showSSHKeyForm.value = true;
  }

  function closeSSHKeyForm() {
    if (sshKeySaving.value) return;
    sshKeyForm.value = createEmptySSHKeyForm();
    sshKeyFormError.value = "";
    sshKeyPassphraseVisible.value = false;
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
      sshKeyForm.value = createEmptySSHKeyForm();
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
        private_key_ref: serverForm.value.private_key_ref,
        host_key_fingerprint: serverForm.value.host_key_fingerprint,
        certificate_path: serverForm.value.certificate_path,
        private_key_path: serverForm.value.private_key_path,
        certificate_chain_path: serverForm.value.certificate_chain_path,
        web_server: serverForm.value.web_server,
        reload_command: parseCommand(serverForm.value.reload_command),
      };
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
    error.value = "";
    notice.value = "";
    try {
      const response = await fetchAdmin(`/api/v1/domains/${item.id}/${action}`, {
        method: "POST",
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "域名操作失败");
      notice.value = successMessage;
      await loadData();
    } catch (err) {
      error.value = err.message || "域名操作失败";
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

  function openSettings() {
    settingsError.value = "";
    settingsForm.value = normalizeSettings(settings.value);
    showSettings.value = true;
  }

  function closeSettings() {
    if (!settingsSaving.value) showSettings.value = false;
  }

  async function saveSettings() {
    settingsError.value = "";
    settingsSaving.value = true;
    try {
      const response = await fetchAdmin("/api/v1/settings", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(settingsForm.value),
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
    event.target.value = "";
    importError.value = "";
  }

  async function importCertificate() {
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
      if (!response.ok) {
        throw new Error(result.error || "导入证书失败");
      }
      importResult.value = result;
      notice.value = `域名 ${importDomain.value.name} 的证书已导入`;
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

  async function saveDomainServers(domainId, serverIds) {
    const response = await fetchAdmin(`/api/v1/domains/${domainId}/servers`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ server_ids: serverIds }),
    });
    const result = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(result.error || "保存域名服务器关联失败");
    domainServers.value = {
      ...domainServers.value,
      [domainId]: result.items || [],
    };
  }

  function parseSANs(value) {
    return value
      .split(/[\n,，]/)
      .map((item) => item.trim())
      .filter(Boolean);
  }

  async function saveDomain() {
    formError.value = "";
    notice.value = "";
    if (!form.value.name.trim()) {
      formError.value = "请输入主域名";
      return;
    }
    if (
      form.value.certificate_type === "san" &&
      parseSANs(form.value.sans).length === 0
    ) {
      formError.value = "SAN 证书至少需要填写一个附加域名";
      return;
    }
    saving.value = true;
    try {
      const payload = {
        name: form.value.name,
        certificate_type: form.value.certificate_type,
        sans: parseSANs(form.value.sans),
        auto_renew: form.value.auto_renew,
        acme_environment: form.value.acme_environment,
        dns_provider: form.value.dns_provider,
      };
      const path = isEditing.value
        ? `/api/v1/domains/${editingId.value}`
        : "/api/v1/domains";
      const response = await fetchAdmin(path, {
        method: isEditing.value ? "PUT" : "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "保存域名失败");
      await saveDomainServers(result.id, form.value.server_ids);
      showForm.value = false;
      notice.value = isEditing.value
        ? "域名配置已更新"
        : "域名已添加，等待申请证书";
      await loadData();
    } catch (err) {
      formError.value = err.message || "保存域名失败";
    } finally {
      saving.value = false;
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
        domainResponse,
        serverResponse,
        sshKeyResponse,
        certificateResponse,
        taskResponse,
        auditResponse,
        settingsResponse,
      ] = await Promise.all([
        fetchAdmin("/api/v1/status"),
        fetchAdmin("/api/v1/domains"),
        fetchAdmin("/api/v1/servers"),
        fetchAdmin("/api/v1/ssh-keys"),
        fetchAdmin("/api/v1/certificates"),
        fetchAdmin("/api/v1/tasks?limit=20"),
        fetchAdmin("/api/v1/audit-logs?limit=20"),
        fetchAdmin("/api/v1/settings"),
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
      ].find((response) => !response.ok);
      if (failedResponse) {
        throw new Error(await responseError(failedResponse, "读取管理数据失败"));
      }
      status.value = await statusResponse.json();
      domains.value = (await domainResponse.json()).items || [];
      servers.value = (await serverResponse.json()).items || [];
      sshKeys.value = (await sshKeyResponse.json()).items || [];
      certificates.value = (await certificateResponse.json()).items || [];
      tasks.value = (await taskResponse.json()).items || [];
      auditLogs.value = (await auditResponse.json()).items || [];
      settings.value = await settingsResponse.json();
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

  onMounted(loadData);
  onBeforeUnmount(() => {
    document.body.style.overflow = previousBodyOverflow;
  });

  return {
    scertLogo,
    status,
    domains,
    servers,
    sshKeys,
    certificates,
    tasks,
    auditLogs,
    settings,
    settingsForm,
    domainServers,
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
    sshKeySaving,
    sshKeyFormError,
    certificateTypeLabels,
    domainStatusLabels,
    httpsModeLabels,
    environmentLabels,
    webServerLabels,
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
    loadData,
    displayLabel,
    formatDate,
    daysRemaining,
    statusClass,
  };
}
