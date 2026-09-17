<script setup>
import { useUserManagement } from "./UserManagement.js";

const {
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
} = useUserManagement();
</script>

<template>
  <section v-if="visible" class="user-management panel">
    <div class="user-management-heading">
      <div>
        <p class="eyebrow">权限与账号</p>
        <h2>用户管理</h2>
        <p class="user-management-description">
          管理登录账号、角色和使用状态。密码只在服务端保存摘要。
        </p>
      </div>
      <div class="user-management-actions">
        <button class="secondary-button" type="button" :disabled="loading" @click="loadUsers">
          {{ loading ? "读取中……" : "刷新列表" }}
        </button>
        <button class="primary-button" type="button" @click="openCreate">添加用户</button>
      </div>
    </div>

    <p v-if="error" class="user-message error-message">{{ error }}</p>
    <p v-if="notice" class="user-message notice-message">{{ notice }}</p>

    <div v-if="loading && users.length === 0" class="user-empty">正在读取用户列表……</div>
    <div v-else-if="users.length === 0" class="user-empty">暂无数据库用户，配置中的超级管理员仍可登录。</div>
    <div v-else class="user-table-wrap">
      <table class="user-table">
        <thead>
          <tr>
            <th>账号</th>
            <th>邮箱</th>
            <th>角色</th>
            <th>状态</th>
            <th>创建时间</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in users" :key="item.id">
            <td><strong>{{ item.username }}</strong></td>
            <td>{{ item.email || "未绑定" }}</td>
            <td><span class="user-badge">{{ roleLabels[item.role] || item.role }}</span></td>
            <td><span class="user-status" :class="item.status">{{ statusLabels[item.status] || item.status }}</span></td>
            <td>{{ new Date(item.created_at).toLocaleString("zh-CN") }}</td>
            <td>
              <span v-if="isPrimaryAccount(item)" class="user-badge">受保护账号</span>
              <div v-else class="user-row-actions">
                <button class="text-button" type="button" @click="openEdit(item)">编辑</button>
                <button class="text-button" type="button" @click="toggleStatus(item)">
                  {{ item.status === "active" ? "禁用" : "启用" }}
                </button>
                <button v-if="item.role !== 'super_admin'" class="text-button danger-text" type="button" @click="deleteUser(item)">删除</button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="showForm" class="modal-backdrop" @click.self="closeForm">
      <section class="modal-card user-modal" role="dialog" aria-modal="true" :aria-label="formTitle">
        <div class="modal-heading">
          <div>
            <p class="eyebrow">账号配置</p>
            <h2>{{ formTitle }}</h2>
          </div>
          <button class="close-button" type="button" aria-label="关闭" @click="closeForm">×</button>
        </div>
        <form @submit.prevent="saveUser">
          <div class="form-grid">
            <label class="field">
              <span>用户名</span>
              <input v-model="form.username" type="text" autocomplete="off" placeholder="3 到 64 个字符" />
            </label>
            <label class="field">
              <span>绑定邮箱 <small>可选</small></span>
              <input v-model="form.email" type="email" autocomplete="off" placeholder="用于邮箱登录" />
            </label>
          </div>
          <label class="field">
            <span>密码 <small>{{ isEditing ? "留空表示不修改，8 到 12 个字符" : "8 到 12 个字符" }}</small></span>
            <input v-model="form.password" type="password" autocomplete="new-password" placeholder="请输入密码" />
          </label>
          <div class="form-grid">
            <label class="field">
              <span>角色</span>
              <select v-model="form.role">
                <option value="user">普通用户</option>
                <option value="admin">管理员</option>
                <option v-if="isEditing && form.role === 'super_admin'" value="super_admin" disabled>超级管理员</option>
              </select>
            </label>
            <label class="field">
              <span>状态</span>
              <select v-model="form.status">
                <option value="active">正常</option>
                <option value="disabled">已禁用</option>
              </select>
            </label>
          </div>
          <p v-if="formError" class="form-error">{{ formError }}</p>
          <div class="modal-actions">
            <button class="secondary-button" type="button" :disabled="saving" @click="closeForm">取消</button>
            <button class="primary-button" type="submit" :disabled="saving">{{ saving ? "保存中……" : "保存用户" }}</button>
          </div>
        </form>
      </section>
    </div>
  </section>
</template>

<style scoped>
@import "./UserManagement.css";
</style>
