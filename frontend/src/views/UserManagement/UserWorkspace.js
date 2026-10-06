import { ref } from "vue";
import { useAccessDenied } from "../AccessDenied/AccessDenied.js";

export const workspaceRoleLabels = {
  super_admin: "超级管理员",
  admin: "管理员",
  user: "普通用户",
};

export function useUserWorkspace() {
  const section = ref("account");
  return { ...useAccessDenied(), section };
}
