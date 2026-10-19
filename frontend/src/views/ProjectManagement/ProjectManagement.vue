<script setup>
import { useProjectManagement } from "./ProjectManagement.js";

const {
  projects, selectedProject, loadingProjects, projectError, projectPageSize,
  projectPage, projectHasMore, pageSizes, loadProjects, selectProject,
  changeProjectPageSize, nextProjectPage, previousProjectPage,
  members, accountChoices, loadingMembers, savingMember, memberError, accountError,
  memberNotice, canManageMembers, memberPageSize, memberPage, memberHasMore,
  memberForm, projectRoles, pendingRevoke, loadMembers, changeMemberPageSize,
  nextMemberPage, previousMemberPage, grantMember, requestRevoke, confirmRevoke, roleLabel,
  canCreateProject, projectEditor, pendingDeleteProject, savingProject,
  lifecycleError, lifecycleNotice, canEditProject, canDeleteProject,
  openProjectEditor, requestDeleteProject, cancelProjectAction, saveProject, confirmDeleteProject,
} = useProjectManagement();
</script>

<template>
  <section class="project-management" aria-label="项目与成员">
    <header class="project-heading">
      <h2>项目与成员</h2>
      <div class="project-actions">
        <button v-if="canCreateProject" class="project-primary" type="button" :disabled="loadingProjects || savingMember || savingProject" @click="openProjectEditor(true)">创建项目</button>
        <button type="button" :disabled="loadingProjects || savingMember || savingProject" @click="loadProjects">刷新项目</button>
      </div>
    </header>
    <p v-if="projectError" class="project-error" role="alert">{{ projectError }}</p>
    <p v-if="lifecycleError" class="project-error" role="alert">{{ lifecycleError }}</p>
    <p v-if="lifecycleNotice" class="project-notice" role="status">{{ lifecycleNotice }}</p>
    <div v-if="projectEditor" class="modal-backdrop project-modal-backdrop" @click.self="cancelProjectAction">
      <section class="modal-card project-modal" role="dialog" aria-modal="true" :aria-label="projectEditor.create ? '创建项目' : '编辑项目'">
        <div class="modal-heading">
          <div>
            <p class="eyebrow">项目管理</p>
            <h2>{{ projectEditor.create ? "创建项目" : "编辑项目" }}</h2>
          </div>
          <button class="close-button" type="button" aria-label="关闭" :disabled="savingProject" @click="cancelProjectAction">×</button>
        </div>
        <form class="project-editor" aria-label="项目资料" @submit.prevent="saveProject">
          <label class="field">
            <span>项目名称</span>
            <input v-model="projectEditor.name" required maxlength="128" autocomplete="off" :disabled="savingProject" />
          </label>
          <label class="field">
            <span>项目说明</span>
            <textarea v-model="projectEditor.description" maxlength="4096" rows="3" :disabled="savingProject"></textarea>
          </label>
          <div class="modal-actions project-actions">
            <button class="secondary-button" type="button" :disabled="savingProject" @click="cancelProjectAction">取消</button>
            <button class="project-primary primary-button" type="submit" :disabled="savingProject || loadingProjects || savingMember">{{ savingProject ? "正在保存……" : "保存项目" }}</button>
          </div>
        </form>
      </section>
    </div>
    <div v-if="pendingDeleteProject" class="project-confirm" role="alert">
      <span>确认删除项目“{{ pendingDeleteProject.name }}”（{{ pendingDeleteProject.id }}）？</span>
      <span class="project-help" tabindex="0" title="默认项目不能删除；存在域名、服务器、绑定或成员时会拒绝删除，不会自动删除关联资源。">ⓘ</span>
      <button class="project-danger" type="button" :disabled="savingProject || loadingProjects || savingMember" @click="confirmDeleteProject">{{ savingProject ? "正在删除……" : "确认删除项目" }}</button>
      <button type="button" :disabled="savingProject" @click="cancelProjectAction">取消</button>
    </div>
    <div class="project-layout" :inert="savingProject" :aria-busy="savingProject">
      <aside class="project-directory" aria-label="可见项目">
        <div class="project-toolbar">
          <h3>我的项目</h3>
          <label class="project-page-size-label">
            <span>每页</span>
            <select :value="projectPageSize" :disabled="loadingProjects || savingMember" @change="changeProjectPageSize($event.target.value)">
              <option v-for="size in pageSizes" :key="size" :value="size">{{ size }}</option>
            </select>
          </label>
        </div>
        <p v-if="loadingProjects" role="status">正在读取项目……</p>
        <p v-else-if="!projects.length && !projectError" class="project-empty">暂无可见项目</p>
        <nav class="project-list" aria-label="选择项目">
          <button
            v-for="project in projects" :key="project.id" type="button"
            :aria-current="selectedProject?.id === project.id ? 'true' : undefined"
            :title="project.description || project.name"
            @click="selectProject(project.id)"
          >
            <strong>{{ project.name }}</strong>
            <small>{{ project.can_manage_members ? "可管理成员" : "项目成员" }}</small>
          </button>
        </nav>
        <div class="project-pagination">
          <button type="button" :disabled="loadingProjects || savingMember || projectPage === 0" @click="previousProjectPage">上一页</button>
          <span>第 {{ projectPage + 1 }} 页</span>
          <button type="button" :disabled="loadingProjects || savingMember || !projectHasMore" @click="nextProjectPage">下一页</button>
        </div>
      </aside>

      <section class="project-members" aria-label="项目成员管理">
        <template v-if="selectedProject">
          <div class="project-toolbar">
            <div class="project-title">
              <h3 :title="selectedProject.description">{{ selectedProject.name }}</h3>
              <small :title="selectedProject.id">项目 ID：{{ selectedProject.id }}</small>
            </div>
            <div class="project-actions">
              <button v-if="canEditProject" type="button" :disabled="loadingProjects || savingMember || savingProject" @click="openProjectEditor(false)">编辑项目</button>
              <button v-if="canDeleteProject" class="project-danger" type="button" :disabled="loadingProjects || savingMember || savingProject" @click="requestDeleteProject">删除项目</button>
              <button v-if="canManageMembers" type="button" :disabled="loadingMembers || savingMember || savingProject" @click="loadMembers">刷新成员</button>
            </div>
          </div>
          <p v-if="memberError" class="project-error" role="alert">{{ memberError }}</p>
          <p v-if="memberNotice" class="project-notice" role="status">{{ memberNotice }}</p>
          <p v-if="!canManageMembers" class="project-empty">当前没有成员管理权限。权限调整后请刷新项目。</p>
          <template v-else>
            <form class="project-grant-form" @submit.prevent="grantMember">
              <label class="project-account-field">
                <span>成员账户 <span class="project-help" tabindex="0" title="仅建议当前可见账户和本页已有成员；其他账户请填写对方提供的账户 ID。项目角色不改变账户的全局角色。">ⓘ</span></span>
                <input v-model="memberForm.userID" list="project-account-options" autocomplete="off" required maxlength="128" placeholder="选择或填写账户 ID" :disabled="savingMember || loadingMembers" />
                <datalist id="project-account-options">
                  <option v-for="account in accountChoices" :key="account.id" :value="account.id">{{ account.username }}</option>
                </datalist>
              </label>
              <label>
                <span class="project-role-field-label">项目角色</span>
                <select v-model="memberForm.role" :disabled="savingMember || loadingMembers">
                  <option v-for="role in projectRoles" :key="role.value" :value="role.value">{{ role.label }}</option>
                </select>
              </label>
              <button class="project-primary" type="submit" :disabled="savingMember || loadingMembers">{{ savingMember ? "正在保存……" : "授予角色" }}</button>
            </form>
            <p v-if="accountError" class="project-error" role="status">{{ accountError }}</p>
            <div v-if="pendingRevoke" class="project-confirm" role="alert">
              <span>确认撤销“{{ pendingRevoke.username }}”的“{{ roleLabel(pendingRevoke.role) }}”角色？</span>
              <span class="project-help" tabindex="0" title="撤销自己的项目管理员角色后会立即失去成员管理权限；其他已授予的角色不受影响。">ⓘ</span>
              <button type="button" :disabled="savingMember" @click="confirmRevoke">确认撤销</button>
              <button type="button" :disabled="savingMember" @click="pendingRevoke = null">取消</button>
            </div>
            <div class="project-member-table" :aria-busy="loadingMembers">
              <table>
                <thead>
                  <tr>
                    <th scope="col" class="col-member">成员</th>
                    <th scope="col" class="col-status">状态</th>
                    <th scope="col" class="col-roles">项目角色</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="member in members" :key="member.user_id">
                    <td class="col-member">
                      <div class="project-member-info">
                        <strong class="project-member-name">{{ member.username }}</strong>
                        <small class="project-member-id" :title="member.user_id">账户 ID：{{ member.user_id }}</small>
                      </div>
                    </td>
                    <td class="col-status">
                      <span class="project-status-badge" :class="member.status">
                        {{ member.status === "active" ? "正常" : "已停用" }}
                      </span>
                    </td>
                    <td class="col-roles">
                      <div class="project-role-list">
                        <span v-for="role in member.roles" :key="role" class="project-role">
                          {{ roleLabel(role) }}
                          <button type="button" :disabled="savingMember || loadingMembers" :aria-label="`撤销 ${member.username} 的${roleLabel(role)}角色`" @click="requestRevoke(member, role)">撤销</button>
                        </span>
                      </div>
                    </td>
                  </tr>
                  <tr v-if="!members.length"><td colspan="3" class="project-empty">{{ loadingMembers ? "正在读取成员……" : "本页暂无成员" }}</td></tr>
                </tbody>
              </table>
            </div>
            <div class="project-pagination project-member-pagination">
              <label class="member-page-size-label">
                <span>每页</span>
                <select :value="memberPageSize" :disabled="savingMember || loadingMembers" @change="changeMemberPageSize($event.target.value)">
                  <option v-for="size in pageSizes" :key="size" :value="size">{{ size }}</option>
                </select>
                <span>位成员</span>
              </label>
              <button type="button" :disabled="savingMember || loadingMembers || memberPage === 0" @click="previousMemberPage">上一页</button>
              <span>第 {{ memberPage + 1 }} 页</span>
              <button type="button" :disabled="savingMember || loadingMembers || !memberHasMore" @click="nextMemberPage">下一页</button>
            </div>
          </template>
        </template>
        <p v-else class="project-empty">请从左侧选择有权访问的项目</p>
      </section>
    </div>
  </section>
</template>

<style scoped src="./ProjectManagement.css"></style>
