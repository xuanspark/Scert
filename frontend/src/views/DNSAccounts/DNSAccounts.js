import { computed, onBeforeUnmount, onMounted, ref } from "vue";

export function dnsVerificationLabel(item) {
  if (item.credential_state === "unavailable") return "凭据不可读取";
  if (item.credential_state === "invalid") return "凭据配置不完整";
  return item.verification_state === "verified" ? "已验证" : "待验证";
}

export function useDNSAccounts() {
  const accounts = ref([]);
  const loading = ref(false);
  const error = ref("");
  const pageSize = ref(10);
  const page = ref(1);
  const pageCount = computed(() => Math.max(1, Math.ceil(accounts.value.length / pageSize.value)));
  const visibleAccounts = computed(() => accounts.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value));
  let requestVersion = 0;
  let controller;

  async function loadAccounts() {
    const version = ++requestVersion;
    controller?.abort();
    controller = new AbortController();
    loading.value = true;
    error.value = "";
    accounts.value = [];
    page.value = 1;
    try {
      const response = await fetch("/api/v1/dns/accounts", {
        credentials: "include",
        cache: "no-store",
        signal: controller.signal,
      });
      if (response.status === 401) throw new Error("登录会话已过期，请重新登录");
      if (response.status === 403) throw new Error("当前账号没有查看 DNS 账户的权限");
      if (!response.ok) throw new Error("DNS 账户加载失败，请稍后重试");
      const data = await response.json();
      if (!Array.isArray(data.items) || !data.items.every((item) => item && typeof item.id === "string")) {
        throw new Error("DNS 账户响应格式不正确，请刷新重试");
      }
      if (version === requestVersion) accounts.value = data.items;
    } catch (reason) {
      if (version === requestVersion) error.value = reason.message || "DNS 账户加载失败，请稍后重试";
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

  return { accounts, loading, error, pageSize, page, pageCount, visibleAccounts, loadAccounts, changePageSize };
}
