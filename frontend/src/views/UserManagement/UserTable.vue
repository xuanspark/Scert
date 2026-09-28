<script setup>
defineProps({
  users: { type: Array, required: true },
  canManage: { type: Boolean, default: false },
  roleLabels: { type: Object, required: true },
  statusLabels: { type: Object, required: true },
});
defineEmits(["edit", "toggle", "delete"]);
</script>

<template>
  <p v-if="!users.length" class="user-group-empty">暂无成员</p>
  <div v-else class="user-table-wrap" tabindex="0" aria-label="用户列表">
    <table class="user-table">
      <thead>
        <tr>
          <th scope="col">账号</th>
          <th scope="col">邮箱</th>
          <th scope="col">角色</th>
          <th scope="col">状态</th>
          <th scope="col">创建时间</th>
          <th v-if="canManage" scope="col">操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="item in users" :key="item.id">
          <td>
            <strong>{{ item.username }}</strong>
            <small class="user-account-id" title="可将此账户 ID 提供给项目管理员，用于加入项目">账户 ID：{{ item.id }}</small>
          </td>
          <td>{{ item.email || "未绑定" }}</td>
          <td><span class="user-badge">{{ roleLabels[item.role] || item.role }}</span></td>
          <td><span class="user-status" :class="item.status">{{ statusLabels[item.status] || item.status }}</span></td>
          <td>{{ new Date(item.created_at).toLocaleString("zh-CN") }}</td>
          <td v-if="canManage">
            <span v-if="item.id === 'primary-super-admin'" class="user-badge">受保护账号</span>
            <div v-else class="user-row-actions">
              <button class="text-button" type="button" @click="$emit('edit', item)">编辑</button>
              <button class="text-button" type="button" @click="$emit('toggle', item)">
                {{ item.status === "active" ? "禁用" : "启用" }}
              </button>
              <button v-if="item.role !== 'super_admin'" class="text-button danger-text" type="button" @click="$emit('delete', item)">删除</button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>

<style scoped src="./UserManagement.css"></style>
