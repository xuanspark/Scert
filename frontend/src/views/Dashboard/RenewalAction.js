import { ref } from "vue";

export function useRenewalAction({ canRunRenewal, error, notice, fetchAdmin, taskMonitor }) {
  const renewalSubmitting = ref(false);
  let idempotencyKey = "";

  async function runRenewalNow() {
    if (!canRunRenewal.value) {
      error.value = "只有超级管理员可以执行续期检查";
      return;
    }
    if (renewalSubmitting.value) return;
    renewalSubmitting.value = true;
    error.value = "";
    notice.value = "";
    try {
      if (!idempotencyKey) {
        const bytes = crypto.getRandomValues(new Uint8Array(16));
        idempotencyKey = `renewal-check-${Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}`;
      }
      const response = await fetchAdmin("/api/v1/renewal/run", {
        method: "POST", headers: { "Idempotency-Key": idempotencyKey },
      });
      const result = await response.json().catch(() => ({}));
      if ((response.ok || response.status === 409 && result.replayed) &&
          result.task?.id && result.task.task_type === "renewal_check" &&
          (response.status === 202 || result.replayed)) {
        idempotencyKey = "";
        notice.value = `续期检查任务已提交：${result.task.id}，请在任务详情查看最终结果`;
        await taskMonitor.openTaskDetails(result.task);
        return;
      }
      if (!response.ok) throw new Error(result.error || "提交续期检查失败");
      throw new Error("服务未返回有效续期任务，请在运行概览确认是否已接收，勿重复提交");
    } catch (err) {
      // 未确认受理结果时保留幂等键，重试只找回原任务，不重放续期。
      error.value = err.message || "无法确认续期检查是否已接收，请先查看运行概览";
    } finally {
      renewalSubmitting.value = false;
    }
  }

  return { runRenewalNow, renewalSubmitting };
}
