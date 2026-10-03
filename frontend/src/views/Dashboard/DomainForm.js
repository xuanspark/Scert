import { computed, onBeforeUnmount, ref } from "vue";
import { useDomainDNSAccount } from "./DomainDNSAccount.js";

function emptyForm() {
  return {
    project_id: "default",
    name: "",
    certificate_type: "single",
    sans: "",
    auto_renew: true,
    acme_environment: "staging",
    dns_provider: "",
    challenge_type: "dns-01",
    server_ids: [],
  };
}

function domainPayload(form) {
  return {
    name: form.name,
    certificate_type: form.certificate_type,
    sans: form.sans.split(/[\n,，]/).map((item) => item.trim()).filter(Boolean),
    auto_renew: form.auto_renew,
    acme_environment: form.acme_environment,
    dns_provider: form.challenge_type === "http-01" ? "" : form.dns_provider,
    challenge_type: form.challenge_type || "dns-01",
  };
}

export function useDomainForm({ fetchAdmin, domains, servers, domainServers, notice, loadData }) {
  const form = ref(emptyForm());
  const editingId = ref("");
  const saving = ref(false);
  const dnsAccount = useDomainDNSAccount({ fetchAdmin, form, saving });
  const showForm = ref(false);
  const formError = ref("");
  const domainBindingsLoading = ref(false);
  const domainBindingsReady = ref(true);
  const domainSaveUncertain = ref(false);
  const isEditing = computed(() => Boolean(editingId.value));
  const formTitle = computed(() => isEditing.value ? "编辑域名配置" : "添加托管域名");
  const domainServerOptions = computed(() => servers.value.filter(
    (server) => Boolean(form.value.project_id) && server.project_id === form.value.project_id,
  ));
  let version = 0;
  let savedPayload = "";
  let createdInForm = false;
  let targetID = "";

  onBeforeUnmount(() => { version += 1; });

  function openCreate() {
    if (saving.value) return;
    version += 1;
    editingId.value = "";
    targetID = "";
    form.value = emptyForm();
    formError.value = "";
    savedPayload = "";
    createdInForm = false;
    domainBindingsLoading.value = false;
    domainBindingsReady.value = true;
    domainSaveUncertain.value = false;
    showForm.value = true;
    return dnsAccount.open();
  }

  async function openEdit(item) {
    if (saving.value) return;
    version += 1;
    editingId.value = item.id;
    targetID = item.id;
    form.value = {
      ...emptyForm(),
      project_id: item.project_id || "",
      name: item.name,
      certificate_type: item.certificate_type,
      sans: (item.sans || []).filter((name) => name !== item.name).join("\n"),
      auto_renew: item.auto_renew !== false,
      acme_environment: item.acme_environment || "staging",
      dns_provider: item.dns_provider || "",
      challenge_type: item.challenge_type || "dns-01",
    };
    formError.value = "";
    savedPayload = "";
    createdInForm = false;
    domainBindingsLoading.value = false;
    domainBindingsReady.value = false;
    domainSaveUncertain.value = false;
    showForm.value = true;
    await Promise.all([reloadDomainBindings(), dnsAccount.open(item)]);
  }

  function closeForm() {
    if (saving.value) return;
    version += 1;
    dnsAccount.cancel();
    domainBindingsLoading.value = false;
    showForm.value = false;
  }

  async function reloadDomainBindings() {
    if (saving.value || domainBindingsLoading.value || !editingId.value) return;
    const currentVersion = version;
    const id = targetID;
    domainBindingsLoading.value = true;
    domainBindingsReady.value = false;
    formError.value = "";
    try {
      const response = await fetchAdmin(`/api/v1/domains/${id}/servers`);
      const result = await response.json();
      if (!response.ok || !Array.isArray(result.items)) {
        throw new Error("读取服务器关联失败，请重新读取后再保存，避免覆盖现有关联");
      }
      if (currentVersion !== version) return;
      form.value.server_ids = result.items.map((server) => server.id);
      domainServers.value = { ...domainServers.value, [id]: result.items };
      domainBindingsReady.value = true;
    } catch {
      if (currentVersion === version) {
        formError.value = "读取服务器关联失败，请重新读取后再保存，避免覆盖现有关联";
      }
    } finally {
      if (currentVersion === version) domainBindingsLoading.value = false;
    }
  }

  async function saveDomain() {
    if (saving.value || domainBindingsLoading.value || domainSaveUncertain.value) return;
    formError.value = "";
    notice.value = "";
    if (!domainBindingsReady.value) {
      formError.value = "请先成功读取服务器关联，再保存域名配置";
      return;
    }
    if (!form.value.project_id) {
      formError.value = "域名项目归属未确认，请刷新列表后重新编辑";
      return;
    }
    const payload = domainPayload(form.value);
    if (payload.challenge_type === "http-01" && (
      payload.certificate_type === "wildcard" ||
      [payload.name, ...payload.sans].some((name) => name.trim().startsWith("*."))
    )) {
      formError.value = "HTTP-01 不支持泛域名，请选择 DNS-01";
      return;
    }
    if (!payload.name.trim()) {
      formError.value = "请输入主域名";
      return;
    }
    if (payload.certificate_type === "san" && payload.sans.length === 0) {
      formError.value = "SAN 证书至少需要填写一个附加域名";
      return;
    }
    const serverIds = [...form.value.server_ids];
    const allowedIds = new Set(domainServerOptions.value.map((server) => server.id));
    if (serverIds.length > 100 || serverIds.some((id) => !allowedIds.has(id))) {
      formError.value = "请选择当前项目内可用的服务器，每个域名最多关联一百台";
      return;
    }
    let dnsSnapshot;
    try {
      dnsSnapshot = dnsAccount.prepare(payload);
    } catch (err) {
      formError.value = err.message;
      return;
    }
    let serialized = JSON.stringify(payload);
    const currentVersion = version;
    let saveStage = "资料";
    saving.value = true;
    try {
      // 资料与关联不是同一事务；已确认保存的资料不因关联失败而重复创建。
      if (savedPayload !== serialized) {
        const existingId = targetID;
        let response;
        try {
          response = await fetchAdmin(existingId ? `/api/v1/domains/${existingId}` : "/api/v1/domains", {
            method: existingId ? "PUT" : "POST",
            headers: { "Content-Type": "application/json" },
            body: !existingId && dnsSnapshot.accountID ? JSON.stringify({
              ...payload, dns_account_id: dnsSnapshot.accountID, dns_account_revision: dnsSnapshot.revision,
            }) : serialized,
          });
        } catch {
          if (currentVersion !== version) return;
          domainSaveUncertain.value = true;
          throw new Error("域名保存结果未确认，请关闭表单并刷新列表确认，暂不重复提交");
        }
        if (currentVersion !== version) return;
        const result = await response.json().catch(() => null);
        if (currentVersion !== version) return;
        if (!response.ok) {
          if (response.status >= 500) domainSaveUncertain.value = true;
          throw new Error(domainSaveUncertain.value
            ? "域名保存结果未确认，请关闭表单并刷新列表确认，暂不重复提交"
            : result?.error || "保存域名失败");
        }
        if (!result || !/^[A-Za-z0-9_-]{1,128}$/.test(result.id || "") ||
            result.project_id !== dnsSnapshot.projectID || (existingId && result.id !== existingId)) {
          domainSaveUncertain.value = true;
          throw new Error("域名保存响应不完整，请关闭表单并刷新列表确认，暂不重复提交");
        }
        if (!existingId && dnsSnapshot.accountID) {
          try {
            dnsSnapshot = dnsAccount.acceptCreated(dnsSnapshot, result);
          } catch (err) {
            domainSaveUncertain.value = true;
            throw err;
          }
        }
        editingId.value = result.id;
        targetID = result.id;
        form.value.project_id = result.project_id;
        createdInForm ||= !existingId;
        savedPayload = serialized;
        const index = domains.value.findIndex((item) => item.id === result.id);
        if (index < 0) domains.value = [...domains.value, result];
        else domains.value = domains.value.map((item) => item.id === result.id ? result : item);
      }
      if (currentVersion !== version) return;
      saveStage = "DNS 账户绑定";
      const dnsResult = await dnsAccount.persist(dnsSnapshot, targetID);
      if (currentVersion !== version) return;
      if (dnsResult) {
        domains.value = domains.value.map((item) => item.id === targetID ? { ...item, ...dnsResult } : item);
        payload.dns_provider = dnsResult.dns_provider;
        serialized = JSON.stringify(payload);
        savedPayload = serialized;
      }
      saveStage = "服务器关联";
      const currentIds = new Set(domainServerOptions.value.map((server) => server.id));
      if (serverIds.some((id) => !currentIds.has(id))) {
        throw new Error("服务器所属项目或可见范围已变化，请重新选择服务器");
      }
      const id = targetID;
      let response;
      try {
        response = await fetchAdmin(`/api/v1/domains/${id}/servers`, {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ server_ids: serverIds }),
        });
      } catch {
        throw new Error("服务器关联保存结果未确认，请保留选择重试或刷新列表确认");
      }
      const result = await response.json().catch(() => null);
      if (currentVersion !== version) return;
      if (!response.ok || !Array.isArray(result?.items)) {
        throw new Error(result?.error || "保存服务器关联结果未确认，可保留选择后重试");
      }
      domainServers.value = { ...domainServers.value, [id]: result.items };
      dnsAccount.cancel();
      showForm.value = false;
      notice.value = createdInForm ? "域名已添加，等待申请证书" : "域名配置已更新";
      try {
        await loadData();
      } catch {
        if (currentVersion === version) notice.value = "域名配置已保存，但列表刷新失败，请手动刷新";
      }
    } catch (err) {
      if (currentVersion === version) {
        const prefix = !savedPayload ? "" : savedPayload === serialized
          ? `域名资料已保存；${saveStage}尚未确认。`
          : "此前域名资料已保存；本次修改尚未完成。";
        formError.value = prefix + (err.message || "保存域名失败");
      }
    } finally {
      if (currentVersion === version) saving.value = false;
    }
  }

  return {
    dnsAccountID: dnsAccount.dnsAccountID,
    dnsBindingLoading: dnsAccount.dnsBindingLoading,
    dnsBindingReady: dnsAccount.dnsBindingReady,
    dnsBindingError: dnsAccount.dnsBindingError,
    dnsBound: dnsAccount.dnsBound,
    domainDNSAccountOptions: dnsAccount.domainDNSAccountOptions,
    dnsUnavailableSelection: dnsAccount.dnsUnavailableSelection,
    reloadDomainDNS: dnsAccount.reloadDomainDNS,
    form, editingId, saving, showForm, formError, isEditing, formTitle,
    domainServerOptions, domainBindingsLoading, domainBindingsReady, domainSaveUncertain,
    openCreate, openEdit, closeForm, saveDomain, reloadDomainBindings,
  };
}
