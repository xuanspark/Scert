import { computed, onBeforeUnmount, ref } from "vue";
import { requestDNS } from "./DNSAccountRequest.js";

const titles = {
  create: "新增 DNS 账户", edit: "编辑 DNS 账户", rename: "修改账户名称", rotate: "轮换 DNS 凭据",
  verify: "验证 DNS 权限", status: "变更账户状态",
};

export function useDNSAccountEditor(projects, providers, loading, refresh) {
  const editor = ref(null);
  const saving = ref(false);
  const mutationError = ref("");
  const notice = ref("");
  const managedProjects = computed(() => projects.value.filter((item) => item.can_manage_dns === true));
  const selectedProvider = computed(() => providers.value.find((item) => item.id === editor.value?.provider));
  const editorTitle = computed(() => titles[editor.value?.action] || "DNS 账户");
  let mounted = true;
  let target = null;

  function canManage(account) {
    return account?.managed === true && Number.isSafeInteger(account.revision) && account.revision > 0
      && managedProjects.value.some((item) => item.id === account.project_id);
  }

  function clearEditor() {
    if (editor.value) editor.value.credentials = {};
    editor.value = null;
    target = null;
  }

  function closeEditor() {
    if (!saving.value) clearEditor();
  }

  function openEditor(action, account = null) {
    if (loading.value || saving.value || !titles[action]) return;
    if (action === "create" ? managedProjects.value.length === 0 : !canManage(account)) return;
    if (action === "verify" && account.status !== "active") return;
    clearEditor();
    mutationError.value = "";
    notice.value = "";
    target = action === "create" ? null : { ...account };
    editor.value = {
      action, name: account?.name || "", project_id: account?.project_id || managedProjects.value[0]?.id || "",
      provider: account?.provider || providers.value[0]?.id || "", credentials: {}, test_domain: "",
      enabled: account?.status !== "active",
    };
  }

  function changeProvider() {
    if (!saving.value && editor.value?.action === "create") editor.value.credentials = {};
  }

  async function saveEditor() {
    const form = editor.value;
    if (!mounted || !form || saving.value || loading.value) return;
    mutationError.value = "";
    if (form.action === "create" ? !managedProjects.value.some((item) => item.id === form.project_id) : !canManage(target)) {
      mutationError.value = "当前项目没有 DNS 管理权限，请刷新列表";
      return;
    }
    const body = form.action === "create" ? {} : { revision: target.revision };
    if (["create", "edit", "rename"].includes(form.action)) {
      const name = form.name.trim();
      if (!name || [...name].length > 128 || /[\u0000-\u001f\u007f-\u009f]/u.test(name)) {
        mutationError.value = "账户名称必须为不含控制字符的 1～128 个字符";
        return;
      }
      body.name = name;
    }
    if (["create", "edit", "rotate"].includes(form.action)) {
      const provider = providers.value.find((item) => item.id === (target?.provider || form.provider));
      if (!provider?.available || !Array.isArray(provider.credential_fields)) {
        mutationError.value = "请选择可用的 DNS 服务商";
        return;
      }
      const credentials = {};
      for (const field of provider.credential_fields) {
        const value = String(form.credentials[field.name] || "").trim();
        if (field.required && form.action !== "edit" && !value) {
          mutationError.value = `请填写${field.label}`;
          return;
        }
        if (value) credentials[field.name] = value;
      }
      if (form.action !== "edit" || Object.keys(credentials).length > 0) body.credentials = credentials;
      if (form.action === "create") Object.assign(body, { provider: provider.id, project_id: form.project_id });
    }
    if (form.action === "verify") {
      body.test_domain = form.test_domain.trim().toLowerCase();
      if (!body.test_domain || /[\s/:*]/u.test(body.test_domain)) {
        mutationError.value = "请填写不含协议、路径或通配符的测试域名";
        return;
      }
    }
    if (form.action === "status") body.enabled = target.status !== "active";
    const suffix = { edit: "", rename: "", rotate: "/credentials", verify: "/verify", status: "/status" };
    const path = "/api/v1/dns/accounts" + (form.action === "create" ? "" : `/${encodeURIComponent(target.id)}${suffix[form.action]}`);
    saving.value = true;
    notice.value = "";
    form.credentials = {};
    try {
      await requestDNS(path, {
        method: ["create", "verify"].includes(form.action) ? "POST" : "PUT",
        body: JSON.stringify(body),
      });
      if (!mounted) return;
      const credentialChanged = Boolean(body.credentials);
      notice.value = ["create", "rotate"].includes(form.action)
        || (form.action === "edit" && credentialChanged)
        ? "账户已保存，请使用有权管理的测试域名执行在线验证" : "账户操作已完成";
    } catch (reason) {
      if (!mounted) return;
      mutationError.value = reason.message;
    } finally {
      // 写请求不自动重放；成功或失败均清空输入并重新读取服务器状态。
      if (body.credentials) body.credentials = {};
      if (mounted) {
        clearEditor();
        await refresh({ internal: true });
        if (mounted) saving.value = false;
      }
    }
  }

  onBeforeUnmount(() => {
    mounted = false;
    clearEditor();
  });
  return {
    editor, saving, mutationError, notice, managedProjects, selectedProvider, editorTitle,
    canManage, openEditor, closeEditor, changeProvider, saveEditor,
  };
}
