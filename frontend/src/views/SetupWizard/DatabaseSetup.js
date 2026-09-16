import { computed, onUnmounted, ref, watch } from "vue";

export function useDatabaseSetup(clock) {
  const databaseForm = ref({
    driver: "sqlite",
    host: "",
    port: 3306,
    database: "scert",
    username: "",
    password: "",
    root_certificate: "",
  });
  const databaseTesting = ref(false);
  const databaseError = ref("");
  const databaseVerification = ref(null);
  let version = 0;
  const databaseVerified = computed(() => {
    clock.value;
    return databaseVerification.value?.verified === true &&
      !!databaseVerification.value?.verification_token &&
      new Date(databaseVerification.value.expires_at).getTime() > Date.now();
  });
  function invalidateDatabase() {
    version += 1;
    databaseVerification.value = null;
    databaseTesting.value = false;
    databaseError.value = "";
  }
  watch(() => databaseForm.value.driver, (driver) => {
    databaseForm.value.port = driver === "postgres" ? 5432 : 3306;
    databaseForm.value.root_certificate = "";
  }, { flush: "sync" });
  watch(databaseForm, invalidateDatabase, { deep: true, flush: "sync" });
  async function testDatabase() {
    invalidateDatabase();
    const current = version;
    databaseTesting.value = true;
    const form = databaseForm.value;
    const payload = form.driver === "sqlite" ? { driver: "sqlite" } : {
      ...form, port: Number(form.port), tls_mode: form.driver === "mysql" ? "true" : "verify-full",
    };
    try {
      const response = await fetch("/api/v1/bootstrap/database/test", {
        method: "POST", credentials: "same-origin",
        headers: { "Content-Type": "application/json" }, body: JSON.stringify(payload),
      });
      const result = await response.json().catch(() => ({}));
      if (current !== version) return;
      if (!response.ok || result.verified !== true || !result.verification_token ||
        result.driver !== payload.driver || !(new Date(result.expires_at).getTime() > Date.now())) {
        throw new Error(result.error || "数据库连接和写权限验证失败。");
      }
      databaseVerification.value = result;
    } catch (err) {
      if (current === version) databaseError.value = err.message || "数据库验证失败。";
    } finally {
      if (current === version) databaseTesting.value = false;
    }
  }
  onUnmounted(invalidateDatabase);
  return { databaseForm, databaseTesting, databaseError, databaseVerification, databaseVerified, testDatabase, invalidateDatabase };
}
