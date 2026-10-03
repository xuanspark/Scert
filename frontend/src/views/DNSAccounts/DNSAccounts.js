import { computed, onBeforeUnmount, onMounted, ref } from "vue";
import { loadDNSProjects, requestDNS } from "./DNSAccountRequest.js";
import { useDNSAccountEditor } from "./DNSAccountEditor.js";

export function dnsVerificationLabel(item) {
  if (item.status === "disabled") return "已停用";
  if (item.credential_state === "unavailable") return "凭据不可读取";
  if (item.credential_state === "invalid") return "凭据配置不完整";
  if (item.verification_state === "failed") return "验证失败";
  return item.verification_state === "verified" ? "已验证" : "待验证";
}

export function dnsProjectLabel(item) {
  const projectID = typeof item?.project_id === "string" ? item.project_id.trim() : "";
  return projectID === "" || projectID === "default" ? "默认项目" : projectID;
}

export function useDNSAccounts() {
  const accounts = ref([]);
  const projects = ref([]);
  const providers = ref([]);
  const loading = ref(false);
  const error = ref("");
  const pageSize = ref(10);
  const page = ref(1);
  const pageCount = computed(() => Math.max(1, Math.ceil(accounts.value.length / pageSize.value)));
  const visibleAccounts = computed(() => accounts.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value));
  let requestVersion = 0;
  let controller;
  const actions = useDNSAccountEditor(projects, providers, loading, loadAccounts);

  async function loadAccounts(options = {}) {
    if (actions.saving.value && options?.internal !== true) return;
    actions.closeEditor();
    const version = ++requestVersion;
    controller?.abort();
    controller = new AbortController();
    loading.value = true;
    error.value = "";
    accounts.value = [];
    projects.value = [];
    providers.value = [];
    page.value = 1;
    try {
      const signal = controller.signal;
      const [data, projectItems, catalog] = await Promise.all([
        requestDNS("/api/v1/dns/accounts", { signal }),
        loadDNSProjects(signal),
        requestDNS("/api/v1/dns/providers", { signal }),
      ]);
      if (!Array.isArray(data.items) || !data.items.every((item) => item && typeof item.id === "string")) {
        throw new Error("DNS 账户响应格式不正确，请刷新重试");
      }
      if (!Array.isArray(catalog.items)) throw new Error("DNS 服务商目录格式不正确，请刷新重试");
      if (version === requestVersion) {
        accounts.value = data.items;
        projects.value = projectItems;
        providers.value = catalog.items.filter((item) => item.available === true);
      }
    } catch (reason) {
      if (version === requestVersion && reason.name !== "AbortError") error.value = reason.message || "DNS 账户加载失败，请稍后重试";
    } finally {
      if (version === requestVersion) loading.value = false;
    }
  }

  function changePageSize() {
    page.value = 1;
  }

  onMounted(loadAccounts);
  onBeforeUnmount(() => {
    requestVersion += 1;
    controller?.abort();
  });

  function projectLabel(item) {
    return projects.value.find((project) => project.id === item.project_id)?.name || dnsProjectLabel(item);
  }

  return { ...actions, accounts, providers, loading, error, pageSize, page, pageCount, visibleAccounts, loadAccounts, changePageSize, projectLabel };
}
