import { computed, onBeforeUnmount, ref } from "vue";

const validID = (value) => typeof value === "string" && /^[A-Za-z0-9_-]{1,128}$/.test(value);

function validBinding(item, domainID, projectID) {
  return item && item.domain_id === domainID && item.project_id === projectID &&
    ((item.id === "" && item.dns_account_id === "") ||
      (validID(item.id) && validID(item.dns_account_id)));
}

export function useDomainDNSAccount({ fetchAdmin, form, saving }) {
  const dnsAccounts = ref([]);
  const dnsAccountID = ref("");
  const dnsBindingLoading = ref(false);
  const dnsBindingReady = ref(false);
  const dnsBindingError = ref("");
  const dnsBound = ref(false);
  let generation = 0;
  let controller;
  let target = null;
  let binding = null;
  let boundProvider = "";

  const domainDNSAccountOptions = computed(() => dnsAccounts.value.filter((account) =>
    account.project_id === target?.projectID && account.managed === true &&
    validID(account.id) && validID(account.provider) &&
    Number.isInteger(account.revision) && account.revision > 0 && account.revision <= 2147483647 &&
    account.status === "active" && account.verification_state === "verified" &&
    account.credential_state === "available",
  ));
  const dnsUnavailableSelection = computed(() => Boolean(dnsAccountID.value) &&
    !domainDNSAccountOptions.value.some((account) => account.id === dnsAccountID.value));

  function cancel() {
    generation += 1;
    controller?.abort();
    controller = null;
    dnsBindingLoading.value = false;
    dnsBindingReady.value = false;
    dnsAccounts.value = [];
    dnsAccountID.value = "";
    dnsBindingError.value = "";
    dnsBound.value = false;
    target = null;
    binding = null;
  }

  async function request(path, options = {}) {
    let response;
    try {
      response = await fetchAdmin(path, { cache: "no-store", ...options });
    } catch {
      throw new Error("网络读取失败，请重新读取 DNS 账户后核对");
    }
    if (!response.ok) {
      const messages = {
        401: "登录已失效，请重新登录",
        403: "无权读取或变更 DNS 账户绑定，请刷新权限后重新打开",
        409: "绑定、账户版本或任务状态已变化，请重新读取后核对，不会自动重试",
        422: "当前验证方式或账户状态不允许绑定，请重新读取后核对",
      };
      throw new Error(messages[response.status] || "DNS 账户请求失败，请重新读取后核对");
    }
    try {
      return await response.json();
    } catch {
      throw new Error("DNS 账户响应格式不正确，请重新读取后核对");
    }
  }

  async function reloadDomainDNS() {
    if (saving.value || !target) return;
    controller?.abort();
    controller = new AbortController();
    const signal = controller.signal;
    const current = ++generation;
    const expected = { ...target };
    dnsBindingLoading.value = true;
    dnsBindingReady.value = false;
    dnsBindingError.value = "";
    dnsAccounts.value = [];
    try {
      const [accounts, result] = await Promise.all([
        request("/api/v1/dns/accounts", { signal }),
        expected.domainID
          ? request(`/api/v1/domains/${expected.domainID}/dns-account`, { signal })
          : Promise.resolve({ id: "", dns_account_id: "", domain_id: "", project_id: expected.projectID }),
      ]);
      if (current !== generation) return;
      if (!Array.isArray(accounts?.items) || !validBinding(result, expected.domainID, expected.projectID)) {
        throw new Error("DNS 账户或绑定响应不完整，请重新读取");
      }
      binding = { ...result };
      dnsAccounts.value = accounts.items.filter((item) => item && typeof item === "object");
      dnsAccountID.value = binding.dns_account_id;
      dnsBound.value = Boolean(binding.id);
      const account = dnsAccounts.value.find((item) => item.id === binding.dns_account_id);
      // 重新读取后以实际绑定服务商为准；不把用户尚未保存的选择当成现有绑定。
      if (account && validID(account.provider)) boundProvider = account.provider;
      if (binding.id) form.value.dns_provider = boundProvider;
      dnsBindingReady.value = true;
    } catch (error) {
      if (current !== generation) return;
      binding = null;
      dnsBindingError.value = error instanceof SyntaxError || error?.name === "TypeError"
        ? "读取 DNS 账户失败，请重新读取；不会清空原绑定"
        : error.message || "读取 DNS 账户失败，请重新读取";
    } finally {
      if (current === generation) dnsBindingLoading.value = false;
    }
  }

  function open(item = {}) {
    cancel();
    target = { domainID: item.id || "", projectID: item.project_id ?? form.value.project_id };
    boundProvider = item.dns_provider || "";
    return reloadDomainDNS();
  }

  function prepare(payload) {
    if (!dnsBindingReady.value || dnsBindingLoading.value || !binding) {
      throw new Error("请先成功读取 DNS 账户和绑定，再保存配置");
    }
    if (form.value.project_id !== target.projectID) {
      throw new Error("域名项目已变化，请关闭后重新打开表单");
    }
    if (payload.challenge_type === "http-01" && (binding.id || dnsAccountID.value)) {
      throw new Error("请先在 DNS 验证方式下解除账户绑定并保存，再切换 HTTP-01");
    }
    const changed = dnsAccountID.value !== binding.dns_account_id;
    const account = domainDNSAccountOptions.value.find((item) => item.id === dnsAccountID.value);
    if (changed && dnsAccountID.value && !account) {
      throw new Error("请选择当前项目内已启用且验证通过的 DNS 账户");
    }
    if (binding.id) payload.dns_provider = boundProvider;
    else if (account) payload.dns_provider = account.provider;
    return Object.freeze({
      generation, domainID: target.domainID, projectID: target.projectID, changed,
      accountID: dnsAccountID.value, revision: account?.revision || 0,
      expectedID: binding.id, provider: account?.provider || "",
    });
  }

  function acceptCreated(snapshot, result) {
    if (snapshot.generation !== generation || snapshot.domainID || !snapshot.accountID ||
        !validID(result?.id) || !validID(result?.dns_binding_id) ||
        result.project_id !== snapshot.projectID || result.dns_account_id !== snapshot.accountID ||
        result.dns_provider !== snapshot.provider) {
      throw new Error("域名初始 DNS 绑定响应不完整，请关闭表单并刷新列表核对");
    }
    binding = {
      id: result.dns_binding_id, domain_id: result.id,
      project_id: result.project_id, dns_account_id: result.dns_account_id,
    };
    target.domainID = result.id;
    dnsAccountID.value = result.dns_account_id;
    dnsBound.value = true;
    boundProvider = snapshot.provider;
    form.value.dns_provider = boundProvider;
    return Object.freeze({ ...snapshot, domainID: result.id, expectedID: result.dns_binding_id, changed: false });
  }

  async function persist(snapshot, domainID) {
    if (snapshot.generation !== generation) return null;
    if (!validID(domainID) || (snapshot.domainID && domainID !== snapshot.domainID)) {
      throw new Error("域名保存目标不一致，请关闭表单后核对");
    }
    target.domainID = domainID;
    if (!snapshot.changed) return null;
    try {
      const result = await request(`/api/v1/domains/${domainID}/dns-account`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          dns_account_id: snapshot.accountID,
          expected_binding_id: snapshot.expectedID,
          account_revision: snapshot.revision,
        }),
      });
      if (snapshot.generation !== generation) return null;
      if (!validBinding(result, domainID, snapshot.projectID) || result.dns_account_id !== snapshot.accountID ||
          (snapshot.accountID && result.id === snapshot.expectedID)) {
        throw new Error("DNS 绑定保存响应不完整，请重新读取后核对");
      }
      binding = { ...result };
      dnsAccountID.value = result.dns_account_id;
      dnsBound.value = Boolean(result.id);
      boundProvider = snapshot.provider;
      form.value.dns_provider = boundProvider;
      return { dns_account_id: result.dns_account_id, dns_binding_id: result.id, dns_provider: boundProvider };
    } catch {
      if (snapshot.generation !== generation) return null;
      // 写入结果可能已提交，不能用旧令牌自动重试，也不能退回系统账户继续保存。
      dnsBindingReady.value = false;
      dnsBindingError.value = "DNS 绑定保存未确认，可能存在权限、版本或在途任务冲突；请重新读取后核对，不会自动重试";
      throw new Error(dnsBindingError.value);
    }
  }

  onBeforeUnmount(cancel);
  return {
    dnsAccountID, dnsBindingLoading, dnsBindingReady, dnsBindingError, dnsBound,
    domainDNSAccountOptions, dnsUnavailableSelection, reloadDomainDNS,
    open, cancel, prepare, acceptCreated, persist,
  };
}
