import { computed, onMounted, ref } from "vue";

export function useDashboard() {
  const scertLogo = "/Scert.png";

  const status = ref(null);
  const domains = ref([]);
  const servers = ref([]);
  const sshKeys = ref([]);
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
    showSSHKeyForm.value = true;
  }

  function closeSSHKeyForm() {
    if (sshKeySaving.value) return;
    sshKeyForm.value = createEmptySSHKeyForm();
    sshKeyFormError.value = "";
    showSSHKeyForm.value = false;
  }

  async function saveSSHKey() {
    sshKeyFormError.value = "";
    sshKeySaving.value = true;
    try {
      const response = await fetch("/api/v1/ssh-keys", {
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
      const response = await fetch(`/api/v1/ssh-keys/${item.id}`, {
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
      const response = await fetch(path, {
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

  async function deleteServer(item) {
    if (!window.confirm(`确定删除服务器“${item.name}”吗？关联域名也会解除。`))
      return;
    try {
      const response = await fetch(`/api/v1/servers/${item.id}`, {
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
    const response = await fetch(`/api/v1/domains/${domainId}/servers`, {
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
      const response = await fetch(path, {
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
      const response = await fetch(`/api/v1/domains/${item.id}`, {
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
      const [statusResponse, domainResponse, serverResponse, sshKeyResponse] =
        await Promise.all([
          fetch("/api/v1/status"),
          fetch("/api/v1/domains"),
          fetch("/api/v1/servers"),
          fetch("/api/v1/ssh-keys"),
        ]);
      if (
        !statusResponse.ok ||
        !domainResponse.ok ||
        !serverResponse.ok ||
        !sshKeyResponse.ok
      )
        throw new Error("服务接口返回失败");
      status.value = await statusResponse.json();
      domains.value = (await domainResponse.json()).items || [];
      servers.value = (await serverResponse.json()).items || [];
      sshKeys.value = (await sshKeyResponse.json()).items || [];
      const associations = await Promise.all(
        domains.value.map(async (item) => {
          const response = await fetch(`/api/v1/domains/${item.id}/servers`);
          return [
            item.id,
            response.ok ? (await response.json()).items || [] : [],
          ];
        }),
      );
      domainServers.value = Object.fromEntries(associations);
    } catch {
      error.value = "无法连接核心服务，请先启动 scert serve";
    } finally {
      loading.value = false;
    }
  }

  onMounted(loadData);

  return {
    scertLogo,
    status,
    domains,
    servers,
    sshKeys,
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
    saveSSHKey,
    deleteSSHKey,
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
