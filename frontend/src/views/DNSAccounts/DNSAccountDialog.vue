<script setup>
import { onBeforeUnmount, onMounted, ref } from "vue";

defineProps({
  editor: { type: Object, required: true },
  title: { type: String, required: true },
  projects: { type: Array, required: true },
  providers: { type: Array, required: true },
  provider: { type: Object, default: null },
  saving: Boolean,
  error: { type: String, default: "" },
});
const emit = defineEmits(["close", "save", "provider-change"]);
const dialog = ref(null);
let previousFocus;
onMounted(() => {
  previousFocus = document.activeElement;
  dialog.value.showModal();
});
onBeforeUnmount(() => previousFocus?.focus?.());
</script>

<template>
  <dialog ref="dialog" class="dns-account-dialog" aria-labelledby="dns-editor-title" @cancel.prevent="emit('close')">
    <form autocomplete="off" :aria-busy="saving" @submit.prevent="emit('save')">
      <header>
        <h3 id="dns-editor-title">{{ title }}</h3>
        <button type="button" :disabled="saving" aria-label="关闭账户编辑" @click="emit('close')">关闭</button>
      </header>
      <fieldset :disabled="saving">
        <label v-if="['create', 'rename'].includes(editor.action)">
          账户名称
          <input v-model="editor.name" autofocus required maxlength="128" aria-label="账户名称" />
        </label>
        <template v-if="editor.action === 'create'">
          <label>
            归属项目
            <select v-model="editor.project_id" required aria-label="归属项目">
              <option v-for="project in projects" :key="project.id" :value="project.id">{{ project.name }}</option>
            </select>
          </label>
          <label>
            DNS 服务商
            <select v-model="editor.provider" required aria-label="DNS 服务商" @change="emit('provider-change')">
              <option v-for="item in providers" :key="item.id" :value="item.id">{{ item.name }}</option>
            </select>
          </label>
        </template>
        <template v-if="['create', 'rotate'].includes(editor.action)">
          <label v-for="field in provider?.credential_fields || []" :key="`${editor.provider}-${field.name}`" :title="field.placeholder">
            {{ field.label }}{{ field.required ? '（必填）' : '（可选）' }}
            <input
              v-model="editor.credentials[field.name]"
              :type="field.secret ? 'password' : 'text'"
              :required="field.required"
              :aria-label="field.label"
              :placeholder="field.placeholder"
              autocomplete="off"
              spellcheck="false"
            />
          </label>
        </template>
        <label v-if="editor.action === 'verify'" title="仅填写你有权管理的域名。验证会创建、查询并清理临时 DNS 记录。">
          授权测试域名
          <input v-model="editor.test_domain" required aria-label="授权测试域名" placeholder="example.com" />
        </label>
        <p v-if="editor.action === 'status'" class="dns-dialog-confirm">
          确认{{ editor.enabled ? '启用' : '停用' }}账户“{{ editor.name }}”？
        </p>
        <p v-if="editor.action === 'rotate'" class="dns-dialog-confirm">
          为“{{ editor.name }}”保存新凭据后，需要重新验证。
        </p>
      </fieldset>
      <p v-if="error" class="dns-dialog-error" role="alert">{{ error }}</p>
      <footer>
        <button type="button" :disabled="saving" @click="emit('close')">取消</button>
        <button type="submit" :disabled="saving">{{ saving ? '处理中…' : editor.action === 'verify' ? '开始在线验证' : '确认保存' }}</button>
      </footer>
    </form>
  </dialog>
</template>

<style scoped src="./DNSAccountDialog.css"></style>
