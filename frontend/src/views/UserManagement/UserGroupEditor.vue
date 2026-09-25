<script setup>
import HelpTip from "../components/HelpTip/HelpTip.vue";
import { useUserGroupEditor } from "./UserGroupEditor.js";

const props = defineProps({
  modelValue: { type: Array, required: true },
  availableGroups: { type: Array, default: () => [] },
  role: { type: String, required: true },
});
const emit = defineEmits(["update:modelValue"]);
const { groupName, groupError, addGroup, removeGroup } = useUserGroupEditor(props, emit);
</script>

<template>
  <fieldset class="group-editor">
    <legend>
      所属分组
      <HelpTip>未选择时归入默认分组。管理员可属于多个分组，普通用户最多一个分组。分组名称区分大小写。</HelpTip>
    </legend>
    <div class="group-selection">
      <span v-if="!modelValue.length" class="user-badge">默认分组</span>
      <button v-for="name in modelValue" :key="name" type="button" :aria-label="`移除分组 ${name}`" @click="removeGroup(name)">
        {{ name }} ×
      </button>
    </div>
    <div class="group-input-row">
      <input
        v-model="groupName"
        type="text"
        list="user-group-options"
        aria-label="选择或新建分组"
        placeholder="选择或输入分组名称"
        maxlength="32"
        @keydown.enter.prevent="addGroup"
      />
      <datalist id="user-group-options">
        <option v-for="name in availableGroups" :key="name" :value="name" />
      </datalist>
      <button type="button" class="secondary-button" @click="addGroup">添加到分组</button>
    </div>
    <p v-if="groupError" class="form-error" role="alert">{{ groupError }}</p>
    <p v-else-if="role === 'user' && modelValue.length > 1" class="form-error" role="alert">普通用户只能属于一个分组，请移除多余分组后保存。</p>
  </fieldset>
</template>

<style scoped src="./UserManagement.css"></style>
