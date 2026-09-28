import { ref } from "vue";
import { useAccessDenied } from "../AccessDenied/AccessDenied.js";

export function useUserWorkspace() {
  const section = ref("account");
  return { ...useAccessDenied(), section };
}
