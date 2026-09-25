import { ref } from "vue";

export function useUserGroupEditor(props, emit) {
  const groupName = ref("");
  const groupError = ref("");
  function addGroup() {
    const name = groupName.value.trim();
    groupError.value = "";
    if (!name || name === "默认分组" || [...name].length > 32 || /[\u0000-\u001f\u007f-\u009f]/u.test(name)) {
      groupError.value = "请输入 1 到 32 个字符的分组名称；默认分组无需添加";
      return;
    }
    const groups = [...new Set([...props.modelValue, name])];
    if (props.role === "user" && groups.length > 1) {
      groupError.value = "普通用户只能属于一个分组，请先移除原分组";
      return;
    }
    if (groups.length > 32) {
      groupError.value = "分组数量不能超过 32 个";
      return;
    }
    emit("update:modelValue", groups);
    groupName.value = "";
  }
  function removeGroup(name) {
    emit("update:modelValue", props.modelValue.filter((item) => item !== name));
    groupError.value = "";
  }
  return { groupName, groupError, addGroup, removeGroup };
}
