import { computed, onUnmounted, ref, watch } from "vue";
import { handleProjectSession, pageSizes, requestProjectAPI } from "./ProjectRequest.js";

export const projectRoles = [
  { value: "project_admin", label: "项目管理员" },
  { value: "certificate_admin", label: "证书管理员" },
  { value: "deployment_admin", label: "部署管理员" },
  { value: "dns_admin", label: "DNS 管理员" },
  { value: "audit_viewer", label: "审计查看者" },
  { value: "read_only", label: "只读用户" },
];

export function useProjectMembers(project) {
  const members = ref([]);
  const accounts = ref([]);
  const loadingMembers = ref(false);
  const savingMember = ref(false);
  const memberError = ref("");
  const accountError = ref("");
  const memberNotice = ref("");
  const accessDenied = ref(false);
  const memberPageSize = ref(10);
  const memberPage = ref(0);
  const cursors = ref([""]);
  const memberHasMore = ref(false);
  const nextCursor = ref("");
  const memberForm = ref({ userID: "", role: "read_only" });
  const pendingRevoke = ref(null);
  const canManageMembers = computed(() => Boolean(project.value?.can_manage_members && !accessDenied.value));
  const accountChoices = computed(() => {
    const choices = new Map(accounts.value.map((item) => [item.id, { id: item.id, username: item.username }]));
    for (const item of members.value) {
      if (item.status === "active") choices.set(item.user_id, { id: item.user_id, username: item.username });
    }
    return [...choices.values()];
  });
  let scope = 0;
  let readGeneration = 0;
  let readController;
  let accountController;

  function clearPage() {
    members.value = [];
    memberHasMore.value = false;
    nextCursor.value = "";
  }

  function fail(error) {
    memberError.value = handleProjectSession(error);
    if (error.status === 401 || error.status === 403) {
      accessDenied.value = true;
      clearPage();
      accounts.value = [];
      pendingRevoke.value = null;
      accountController?.abort();
    }
  }

  async function loadMembers() {
    if (!canManageMembers.value) return;
    const current = scope;
    const read = ++readGeneration;
    readController?.abort();
    readController = new AbortController();
    loadingMembers.value = true;
    memberError.value = "";
    clearPage();
    const query = new URLSearchParams({ page_size: String(memberPageSize.value), after: cursors.value[memberPage.value] });
    try {
      const result = await requestProjectAPI(`/api/v1/projects/${encodeURIComponent(project.value.id)}/members?${query}`, { signal: readController.signal });
      if (current !== scope || read !== readGeneration || !canManageMembers.value) return;
      members.value = result.items || [];
      memberHasMore.value = Boolean(result.has_more && result.next_cursor);
      nextCursor.value = result.next_cursor || "";
    } catch (error) {
      if (current === scope && read === readGeneration && error.name !== "AbortError") fail(error);
    } finally {
      if (current === scope && read === readGeneration) loadingMembers.value = false;
    }
  }

  async function loadAccounts() {
    const current = scope;
    accountController = new AbortController();
    try {
      // 复用现有账户可见性接口，不为项目管理员扩大用户目录权限。
      const result = await requestProjectAPI("/api/v1/users", { signal: accountController.signal });
      if (current !== scope || !canManageMembers.value) return;
      accounts.value = (result.items || []).filter((item) => item.status === "active");
    } catch (error) {
      if (current !== scope || !canManageMembers.value || error.name === "AbortError") return;
      if (error.status === 401) fail(error);
      else accountError.value = "账户建议暂不可用，仍可填写对方提供的账户 ID";
    }
  }

  async function changeMemberPageSize(value) {
    if (savingMember.value || !pageSizes.includes(Number(value))) return;
    memberPageSize.value = Number(value);
    memberPage.value = 0;
    cursors.value = [""];
    pendingRevoke.value = null;
    await loadMembers();
  }

  async function nextMemberPage() {
    if (loadingMembers.value || savingMember.value || !memberHasMore.value) return;
    cursors.value = [...cursors.value.slice(0, memberPage.value + 1), nextCursor.value];
    memberPage.value += 1;
    pendingRevoke.value = null;
    await loadMembers();
  }

  async function previousMemberPage() {
    if (loadingMembers.value || savingMember.value || memberPage.value === 0) return;
    memberPage.value -= 1;
    pendingRevoke.value = null;
    await loadMembers();
  }

  async function mutateMember(userID, role, revoke) {
    if (!canManageMembers.value || savingMember.value || loadingMembers.value) return;
    if (!/^[a-zA-Z0-9_-]{1,128}$/.test(userID) || !projectRoles.some((item) => item.value === role)) {
      memberError.value = "请选择有效的账户 ID 和项目角色";
      return;
    }
    const current = scope;
    const base = `/api/v1/projects/${encodeURIComponent(project.value.id)}/members`;
    savingMember.value = true;
    memberError.value = "";
    memberNotice.value = "";
    try {
      await requestProjectAPI(revoke ? `${base}/${encodeURIComponent(userID)}/roles/${encodeURIComponent(role)}` : base, {
        method: revoke ? "DELETE" : "POST",
        ...(revoke ? {} : { body: JSON.stringify({ user_id: userID, role }) }),
      });
      if (current !== scope) return;
      memberNotice.value = revoke ? "项目角色已撤销" : "项目角色已授予";
      memberForm.value = { userID: "", role: "read_only" };
      pendingRevoke.value = null;
      memberPage.value = 0;
      cursors.value = [""];
      await loadMembers();
    } catch (error) {
      if (current !== scope) return;
      fail(error);
      if (error.status === 409) {
        const conflict = memberError.value;
        await loadMembers();
        if (current === scope && canManageMembers.value && !memberError.value) memberError.value = conflict;
      }
    } finally {
      if (current === scope) savingMember.value = false;
    }
  }

  function grantMember() {
    return mutateMember(memberForm.value.userID.trim(), memberForm.value.role, false);
  }

  function requestRevoke(member, role) {
    if (!canManageMembers.value || savingMember.value || loadingMembers.value || !member.roles.includes(role)) return;
    pendingRevoke.value = { userID: member.user_id, username: member.username, role };
  }

  function confirmRevoke() {
    const target = pendingRevoke.value;
    if (target) return mutateMember(target.userID, target.role, true);
  }

  watch(project, () => {
    scope += 1;
    readGeneration += 1;
    readController?.abort();
    accountController?.abort();
    clearPage();
    accounts.value = [];
    memberError.value = "";
    memberNotice.value = "";
    accountError.value = "";
    accessDenied.value = false;
    savingMember.value = false;
    loadingMembers.value = false;
    memberPage.value = 0;
    cursors.value = [""];
    memberForm.value = { userID: "", role: "read_only" };
    pendingRevoke.value = null;
    if (canManageMembers.value) {
      void loadMembers();
      void loadAccounts();
    }
  }, { immediate: true, flush: "sync" });

  onUnmounted(() => {
    scope += 1;
    readGeneration += 1;
    readController?.abort();
    accountController?.abort();
  });

  return {
    members, accountChoices, loadingMembers, savingMember, memberError, accountError,
    memberNotice, canManageMembers, memberPageSize, memberPage, memberHasMore, memberForm,
    projectRoles, pendingRevoke, loadMembers, changeMemberPageSize, nextMemberPage,
    previousMemberPage, grantMember, requestRevoke, confirmRevoke,
    roleLabel: (role) => projectRoles.find((item) => item.value === role)?.label || "未知角色",
  };
}
