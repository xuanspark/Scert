import { computed, onUnmounted, ref, watch } from "vue";
import { handleProjectSession, requestProjectAPI } from "./ProjectRequest.js";

export function useProjectLifecycle(project, canCreateProject, busy, refresh, savingProject = ref(false)) {
  const projectEditor = ref(null);
  const pendingDeleteProject = ref(null);
  const lifecycleError = ref("");
  const lifecycleNotice = ref("");
  const canEditProject = computed(() => Boolean(project.value?.can_manage_project));
  const canDeleteProject = computed(() => Boolean(project.value?.can_delete_project));
  let generation = 0;
  let refreshing = false;

  function clear() {
    generation += 1;
    projectEditor.value = null;
    pendingDeleteProject.value = null;
    savingProject.value = false;
    lifecycleError.value = "";
    lifecycleNotice.value = "";
  }

  function openProjectEditor(create = false) {
    if (busy.value || savingProject.value || !(create ? canCreateProject.value : canEditProject.value)) return;
    lifecycleError.value = "";
    lifecycleNotice.value = "";
    pendingDeleteProject.value = null;
    projectEditor.value = {
      create, id: create ? "" : project.value.id,
      name: create ? "" : project.value.name,
      description: create ? "" : project.value.description || "",
    };
  }

  function requestDeleteProject() {
    if (busy.value || savingProject.value || !canDeleteProject.value) return;
    projectEditor.value = null;
    lifecycleError.value = "";
    lifecycleNotice.value = "";
    pendingDeleteProject.value = { id: project.value.id, name: project.value.name };
  }

  function cancelProjectAction() {
    if (savingProject.value) return;
    projectEditor.value = null;
    pendingDeleteProject.value = null;
    lifecycleError.value = "";
  }

  async function mutateProject(action, id, body) {
    if (busy.value || savingProject.value) return;
    const current = generation;
    savingProject.value = true;
    lifecycleError.value = "";
    lifecycleNotice.value = "";
    try {
      const result = await requestProjectAPI(`/api/v1/projects${action === "create" ? "" : `/${encodeURIComponent(id)}`}`, {
        method: { create: "POST", update: "PUT", delete: "DELETE" }[action],
        body: JSON.stringify(body),
      });
      if (current !== generation) return;
      projectEditor.value = null;
      pendingDeleteProject.value = null;
      lifecycleNotice.value = action === "create"
        ? `项目已创建（ID：${result.id}）。已返回项目首页，未显示时可翻页查看。`
        : action === "delete" ? "项目已删除，已返回项目首页" : "项目资料已更新";
      refreshing = true;
      await refresh(action);
    } catch (error) {
      if (current !== generation) return;
      lifecycleError.value = error.status ? handleProjectSession(error) : "请求结果未确认，请刷新项目后核对，勿重复提交";
      if (error.status === 403 || error.status === 409) {
        projectEditor.value = null;
        pendingDeleteProject.value = null;
        refreshing = true;
        await refresh("conflict");
      }
    } finally {
      refreshing = false;
      if (current === generation) savingProject.value = false;
    }
  }

  function saveProject() {
    const form = projectEditor.value;
    if (!form || !(form.create ? canCreateProject.value : canEditProject.value && form.id === project.value?.id)) return;
    const name = form.name.trim();
    const description = form.description.trim();
    if (!name || [...name].length > 128 || [...description].length > 4096
      || /[\u0000-\u001f\u007f-\u009f]/.test(name)
      || /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f]/.test(description)) {
      lifecycleError.value = "请填写有效项目名称（最多 128 字），说明最多 4096 字";
      return;
    }
    return mutateProject(form.create ? "create" : "update", form.id, { name, description });
  }

  function confirmDeleteProject() {
    const target = pendingDeleteProject.value;
    if (!target || !canDeleteProject.value || target.id !== project.value?.id) return;
    return mutateProject("delete", target.id, { confirmation: target.id });
  }

  watch(project, () => { if (!refreshing) clear(); }, { flush: "sync" });
  onUnmounted(clear);

  return {
    projectEditor, pendingDeleteProject, savingProject, lifecycleError, lifecycleNotice,
    canEditProject, canDeleteProject, openProjectEditor, requestDeleteProject,
    cancelProjectAction, saveProject, confirmDeleteProject,
  };
}
