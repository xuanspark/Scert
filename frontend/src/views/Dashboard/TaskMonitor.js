import { onBeforeUnmount, onMounted, ref } from "vue";

const terminalStatuses = new Set(["success", "failed", "cancelled", "partial_success"]);

export function taskIsPending(task) {
  return !terminalStatuses.has(task?.status);
}

export function latestValidationResult(task, attempts, results) {
  if (task.task_type !== "certificate_validate" || task.status !== "success") return null;
  const latest = attempts.reduce((value, item) =>
    !value || item.attempt_number > value.attempt_number ? item : value, null);
  if (!latest || latest.status !== "success") return null;
  const stored = results.find((item) => item.task_id === task.id &&
    item.attempt_id === latest.id && item.attempt_number === latest.attempt_number);
  return Array.isArray(stored?.validation?.domains) ? stored.validation : null;
}

export function latestDeploymentReport(task, attempts, results, reports = []) {
  const latest = Math.max(0, ...attempts.map((item) => item.attempt_number || 0));
  if (taskIsPending(task) || latest === 0) return null;
  const attempt = attempts.find((item) => item.attempt_number === latest);
  const stored = reports.find((item) => item.task_id === task.id &&
    item.attempt_id === attempt.id && item.attempt_number === latest);
  if (stored && Array.isArray(stored.report?.results)) {
    return { ...stored.report, status: task.status };
  }
  const items = results.filter((item) => item.attempt_number === latest);
  if (taskIsPending(task) || latest === 0 || items.length === 0) return null;
  return {
    status: task.status,
    total: items.length,
    success: items.filter((item) => item.status === "success").length,
    failed: items.filter((item) => item.status !== "success").length,
    results: items,
  };
}

// 查询失败只表示暂时无法确认状态，不能据此重发任务或宣布执行失败。
export function useTaskMonitor({ fetchAdmin, tasks, deploymentResult, showTaskDetails,
  taskDetails, taskAttempts, taskDetailsLoading, taskDetailsError, refreshResources }) {
  const taskQueryError = ref("");
  const taskDeploymentReport = ref(null);
  const taskValidationResult = ref(null);
  let selectedID = "";
  let stopped = false;
  let timer;
  let controller;
  let inFlight;
  let resourcesDirty = false;

  async function readJSON(path, signal) {
    const response = await fetchAdmin(path, { signal });
    const result = await response.json().catch(() => null);
    if (!response.ok || !result) {
      const error = new Error("任务状态查询失败，将自动重试；请勿重复提交任务");
      error.status = response.status;
      throw error;
    }
    return result;
  }

  async function readTask(id, signal) {
    const path = `/api/v1/tasks/${encodeURIComponent(id)}`;
    const task = await readJSON(path, signal);
    if (task.id !== id || !task.status) throw new Error("任务状态响应不完整，将自动重试");
    const [attempts, results, validations] = await Promise.all([
      readJSON(`${path}/attempts`, signal),
      readJSON(`${path}/deployment-results`, signal),
      task.task_type === "certificate_validate"
        ? readJSON(`${path}/validation-results`, signal) : Promise.resolve({ items: [] }),
    ]);
    if (!Array.isArray(attempts.items) || !Array.isArray(results.items) || !Array.isArray(validations.items)) {
      throw new Error("任务执行记录响应不完整，将自动重试");
    }
    const latestAttempt = attempts.items.reduce((latest, item) =>
      !latest || item.attempt_number > latest.attempt_number ? item : latest, null);
    if (!taskIsPending(task) && latestAttempt && latestAttempt.status !== task.status) {
      throw new Error("任务记录正在更新，将重新查询一致的执行结果");
    }
    if (results.reports !== undefined && !Array.isArray(results.reports)) {
      throw new Error("完整部署报告响应不完整，将自动重试");
    }
    return {
      task,
      attempts: attempts.items,
      report: latestDeploymentReport(task, attempts.items, results.items, results.reports),
      validation: latestValidationResult(task, attempts.items, validations.items),
    };
  }

  async function poll() {
    const activeController = new AbortController();
    controller = activeController;
    const signal = activeController.signal;
    const timeout = setTimeout(() => activeController.abort(), 10000);
    const detailID = selectedID;
    const deploymentID = deploymentResult.value?.pending ? deploymentResult.value.taskID : "";
    try {
      const list = await readJSON("/api/v1/tasks?limit=20", signal);
      if (!Array.isArray(list.items)) throw new Error("任务列表响应不完整，将自动重试");
      if (stopped) return;
      const previous = new Map(tasks.value.map((item) => [item.id, item]));
      if (list.items.some((item) => previous.has(item.id) &&
          taskIsPending(previous.get(item.id)) && !taskIsPending(item))) resourcesDirty = true;
      tasks.value = list.items;
      taskQueryError.value = "";
      const ids = [...new Set([detailID, deploymentID].filter(Boolean))];
      await Promise.all(ids.map(async (id) => {
        try {
          const snapshot = await readTask(id, signal);
          if (stopped) return;
          if (selectedID === id && showTaskDetails.value) {
            taskDetails.value = snapshot.task;
            taskAttempts.value = snapshot.attempts;
            taskDeploymentReport.value = snapshot.report;
            taskValidationResult.value = snapshot.validation;
            taskDetailsError.value = "";
          }
          if (deploymentResult.value?.taskID === id) {
            if (deploymentResult.value.pending && !taskIsPending(snapshot.task)) resourcesDirty = true;
            deploymentResult.value = {
              ...deploymentResult.value,
              pending: taskIsPending(snapshot.task),
              taskStatus: snapshot.task.status,
              taskMessage: snapshot.task.message || "",
              report: snapshot.report,
              certificateImported: snapshot.report?.certificate_imported === true,
              queryError: "",
            };
          }
        } catch (error) {
          if (stopped) return;
          const denied = error.status === 401 || error.status === 403;
          const message = denied ? "当前已无权读取此任务，请联系项目管理员"
            : "暂时无法查询任务状态，将自动重试；这不代表任务失败，请勿重复提交";
          if (selectedID === id) {
            taskDetailsError.value = message;
            if (denied) {
              taskDetails.value = null;
              taskAttempts.value = [];
              taskDeploymentReport.value = null;
              taskValidationResult.value = null;
            }
          }
          if (deploymentResult.value?.taskID === id) {
            deploymentResult.value.queryError = message;
            if (denied) {
              deploymentResult.value.report = null;
              deploymentResult.value.certificateImported = false;
              deploymentResult.value.taskMessage = "";
              deploymentResult.value.taskStatus = "";
            }
          }
        }
      }));
      if (!stopped && resourcesDirty && refreshResources) {
        await refreshResources(signal);
        resourcesDirty = false;
      }
    } catch (error) {
      if (!stopped) {
        if (error.status === 401 || error.status === 403) {
          tasks.value = [];
          taskDetails.value = null;
          taskAttempts.value = [];
          taskDeploymentReport.value = null;
          taskValidationResult.value = null;
          if (deploymentResult.value) {
            deploymentResult.value.report = null;
            deploymentResult.value.certificateImported = false;
            deploymentResult.value.taskMessage = "";
          }
        }
        taskQueryError.value = "任务状态查询失败，将自动重试；请勿重复提交任务";
        if (selectedID === detailID && detailID) taskDetailsError.value = taskQueryError.value;
        if (deploymentID && deploymentResult.value?.taskID === deploymentID) {
          deploymentResult.value.queryError = taskQueryError.value;
        }
      }
    } finally {
      clearTimeout(timeout);
      if (!stopped && selectedID === detailID) taskDetailsLoading.value = false;
    }
  }

  function refreshTaskStatus() {
    if (stopped) return Promise.resolve();
    if (!inFlight) inFlight = poll().finally(() => { inFlight = null; });
    return inFlight;
  }

  async function openTaskDetails(item) {
    selectedID = item.id;
    showTaskDetails.value = true;
    taskDetails.value = null;
    taskAttempts.value = [];
    taskDeploymentReport.value = null;
    taskValidationResult.value = null;
    taskDetailsError.value = "";
    taskDetailsLoading.value = true;
    await refreshTaskStatus();
    // 用户切换详情时，旧请求不能覆盖新选择；等待旧查询结束后再读取新任务。
    if (!stopped && selectedID === item.id && taskDetailsLoading.value) await refreshTaskStatus();
  }

  function closeTaskDetails() {
    selectedID = "";
    showTaskDetails.value = false;
    taskDetails.value = null;
    taskAttempts.value = [];
    taskDeploymentReport.value = null;
    taskValidationResult.value = null;
    taskDetailsError.value = "";
    taskDetailsLoading.value = false;
  }

  onMounted(() => { timer = window.setInterval(refreshTaskStatus, 3000); });
  onBeforeUnmount(() => {
    stopped = true;
    window.clearInterval(timer);
    controller?.abort();
  });

  return { taskQueryError, taskDeploymentReport, taskValidationResult,
    refreshTaskStatus, openTaskDetails, closeTaskDetails };
}
