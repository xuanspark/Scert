import { computed, onMounted, onUnmounted, ref } from "vue";
import { handleProjectSession, pageSizes, requestProjectAPI } from "./ProjectRequest.js";
import { useProjectMembers } from "./ProjectMembers.js";
import { useProjectLifecycle } from "./ProjectLifecycle.js";

export function useProjectManagement() {
  const projects = ref([]);
  const selectedProject = ref(null);
  const loadingProjects = ref(false);
  const projectError = ref("");
  const projectPageSize = ref(5);
  const projectCursors = ref([""]);
  const projectPage = ref(0);
  const projectHasMore = ref(false);
  const projectNextCursor = ref("");
  const canCreateProject = ref(false);
  const savingProject = ref(false);
  const members = useProjectMembers(selectedProject, savingProject);
  const lifecycle = useProjectLifecycle(selectedProject, canCreateProject,
    computed(() => loadingProjects.value || members.savingMember.value), refreshAfterMutation, savingProject);
  let generation = 0;
  let controller;

  async function loadProjects(options = {}) {
    if (members.savingMember.value || (lifecycle.savingProject.value && options.internal !== true)) return;
    const current = ++generation;
    controller?.abort();
    controller = new AbortController();
    loadingProjects.value = true;
    projectError.value = "";
    const selectedID = selectedProject.value?.id;
    projects.value = [];
    selectedProject.value = null;
    projectHasMore.value = false;
    projectNextCursor.value = "";
    canCreateProject.value = false;
    const query = new URLSearchParams({
      page_size: String(projectPageSize.value),
      after: projectCursors.value[projectPage.value],
    });
    try {
      const result = await requestProjectAPI(`/api/v1/projects?${query}`, { signal: controller.signal });
      if (current !== generation) return;
      projects.value = result.items || [];
      canCreateProject.value = Boolean(result.can_create_project);
      projectHasMore.value = Boolean(result.has_more && result.next_cursor);
      projectNextCursor.value = result.next_cursor || "";
      selectedProject.value = projects.value.find((item) => item.id === selectedID) || projects.value[0] || null;
    } catch (error) {
      if (current === generation && error.name !== "AbortError") projectError.value = handleProjectSession(error);
    } finally {
      if (current === generation) loadingProjects.value = false;
    }
  }

  async function refreshAfterMutation(action) {
    if (action === "create" || action === "delete") {
      projectPage.value = 0;
      projectCursors.value = [""];
    }
    await loadProjects({ internal: true });
  }

  function selectProject(id) {
    if (loadingProjects.value || lifecycle.savingProject.value || members.savingMember.value || selectedProject.value?.id === id) return;
    selectedProject.value = projects.value.find((item) => item.id === id) || null;
  }

  async function changeProjectPageSize(value) {
    if (lifecycle.savingProject.value || members.savingMember.value || !pageSizes.includes(Number(value))) return;
    projectPageSize.value = Number(value);
    projectPage.value = 0;
    projectCursors.value = [""];
    await loadProjects();
  }

  async function nextProjectPage() {
    if (loadingProjects.value || lifecycle.savingProject.value || members.savingMember.value || !projectHasMore.value) return;
    projectCursors.value = [...projectCursors.value.slice(0, projectPage.value + 1), projectNextCursor.value];
    projectPage.value += 1;
    await loadProjects();
  }

  async function previousProjectPage() {
    if (loadingProjects.value || lifecycle.savingProject.value || members.savingMember.value || projectPage.value === 0) return;
    projectPage.value -= 1;
    await loadProjects();
  }

  onMounted(loadProjects);
  onUnmounted(() => {
    generation += 1;
    controller?.abort();
  });

  return {
    ...members, ...lifecycle, canCreateProject, projects, selectedProject, loadingProjects, projectError,
    projectPageSize, projectPage, projectHasMore, pageSizes,
    loadProjects, selectProject, changeProjectPageSize, nextProjectPage, previousProjectPage,
  };
}
