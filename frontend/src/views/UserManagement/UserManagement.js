import { computed, onMounted, ref } from "vue";

const roleLabels = {
  super_admin: "超级管理员",
  admin: "管理员",
  user: "普通用户",
};

const statusLabels = {
  active: "正常",
  disabled: "已禁用",
};

export function groupUsers(users) {
  const groups = new Map();
  const defaults = { name: "默认分组", isDefault: true, users: [] };
  for (const user of users) {
    const names = [...new Set(user.groups || [])];
    if (!names.length) defaults.users.push(user);
    for (const name of names) {
      if (!groups.has(name)) groups.set(name, { name, isDefault: false, users: [] });
      groups.get(name).users.push(user);
    }
  }
  return [defaults, ...[...groups.values()].sort((a, b) => a.name.localeCompare(b.name, "zh-CN"))];
}

export function useUserManagement() {
  const visible = ref(true);
  const currentRole = ref("");
  const canManage = computed(() => currentRole.value === "super_admin");
  const showGroups = computed(() => ["super_admin", "admin"].includes(currentRole.value));
  const groupedUsers = computed(() => groupUsers(users.value));
  const availableGroups = computed(() => groupedUsers.value.filter((group) => !group.isDefault).map((group) => group.name));
  const users = ref([]);
  const loading = ref(false);
  const saving = ref(false);
  const error = ref("");
  const notice = ref("");
  const showForm = ref(false);
  const editingId = ref("");
  const editingRole = ref("");
  const formError = ref("");
  const form = ref(createEmptyForm());
  const isEditing = computed(() => Boolean(editingId.value));
  const formTitle = computed(() => (isEditing.value ? "编辑用户" : "添加用户"));

  async function request(path, options = {}) {
    const response = await fetch(path, {
      ...options,
      credentials: "include",
      headers: {
        ...(options.body ? { "Content-Type": "application/json" } : {}),
        ...(options.headers || {}),
      },
    });
    if (response.status === 401) {
      window.location.assign("/login");
      throw new Error("登录会话已过期，请重新登录");
    }
    return response;
  }

  async function loadUsers() {
    loading.value = true;
    error.value = "";
    try {
      const response = await request("/api/v1/users");
      if (response.status === 403 || response.status === 404) {
        visible.value = false;
        return;
      }
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "读取用户列表失败");
      users.value = result.items || [];
      currentRole.value = result.current_role || "";
      visible.value = true;
    } catch (err) {
      if (err.message !== "登录会话已过期，请重新登录") {
        error.value = err.message || "读取用户列表失败";
      }
    } finally {
      loading.value = false;
    }
  }

  function createEmptyForm() {
    return {
      username: "",
      email: "",
      password: "",
      role: "user",
      status: "active",
      groups: [],
    };
  }

  function openCreate() {
    if (!canManage.value) return;
    editingId.value = "";
    editingRole.value = "";
    form.value = createEmptyForm();
    formError.value = "";
    showForm.value = true;
  }

  function openEdit(item) {
    if (!canManage.value || isPrimaryAccount(item)) return;
    editingId.value = item.id;
    editingRole.value = item.role;
    form.value = {
      username: item.username || "",
      email: item.email || "",
      password: "",
      role: item.role || "user",
      status: item.status || "active",
      groups: [...(item.groups || [])],
    };
    formError.value = "";
    showForm.value = true;
  }

  function closeForm() {
    if (!saving.value) showForm.value = false;
  }

  async function saveUser() {
    formError.value = "";
    if (!canManage.value) {
      formError.value = "只有超级管理员可以修改用户";
      return;
    }
    if (editingId.value === "primary-super-admin") {
      formError.value = "主超级管理员不能通过用户管理修改";
      return;
    }
    const payload = { ...form.value };
    if (payload.role.trim().toLowerCase() === "user" && payload.groups.length > 1) {
      formError.value = "普通用户只能属于一个分组，请保留一个分组或清空为默认分组";
      return;
    }
    if (payload.role.trim().toLowerCase() === "super_admin" && editingRole.value !== "super_admin") {
      formError.value = "不能新增或提升为超级管理员";
      return;
    }
    if (!payload.password) delete payload.password;
    if (!payload.username.trim()) {
      formError.value = "请输入用户名";
      return;
    }
    if (!isEditing.value && !payload.password) {
      formError.value = "请输入密码";
      return;
    }
    saving.value = true;
    try {
      const path = isEditing.value
        ? `/api/v1/users/${editingId.value}`
        : "/api/v1/users";
      const response = await request(path, {
        method: isEditing.value ? "PUT" : "POST",
        body: JSON.stringify(payload),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "保存用户失败");
      showForm.value = false;
      notice.value = isEditing.value ? "用户信息已更新" : "用户已创建";
      await loadUsers();
    } catch (err) {
      formError.value = err.message || "保存用户失败";
    } finally {
      saving.value = false;
    }
  }

  async function toggleStatus(item) {
    if (!canManage.value || isPrimaryAccount(item)) return;
    const nextStatus = item.status === "active" ? "disabled" : "active";
    if (item.role === "super_admin" && nextStatus === "disabled") {
      notice.value = "最后一个超级管理员不能被禁用";
      return;
    }
    try {
      const response = await request(`/api/v1/users/${item.id}`, {
        method: "PUT",
        body: JSON.stringify({
          username: item.username,
          email: item.email || "",
          role: item.role,
          status: nextStatus,
        }),
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "更新用户状态失败");
      notice.value = nextStatus === "active" ? "用户已启用" : "用户已禁用";
      await loadUsers();
    } catch (err) {
      error.value = err.message || "更新用户状态失败";
    }
  }

  async function deleteUser(item) {
    if (!canManage.value) return;
    if (isPrimaryAccount(item)) return;
    if (!window.confirm(`确定删除用户“${item.username}”吗？`)) return;
    try {
      const response = await request(`/api/v1/users/${item.id}`, {
        method: "DELETE",
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(result.error || "删除用户失败");
      notice.value = "用户已删除";
      await loadUsers();
    } catch (err) {
      error.value = err.message || "删除用户失败";
    }
  }

  function isPrimaryAccount(item) {
    return item.id === "primary-super-admin";
  }

  onMounted(loadUsers);

  return {
    currentRole,
    canManage,
    showGroups,
    groupedUsers,
    availableGroups,
    visible,
    users,
    loading,
    saving,
    error,
    notice,
    showForm,
    form,
    formError,
    formTitle,
    isEditing,
    roleLabels,
    statusLabels,
    isPrimaryAccount,
    openCreate,
    openEdit,
    closeForm,
    saveUser,
    toggleStatus,
    deleteUser,
    loadUsers,
  };
}
