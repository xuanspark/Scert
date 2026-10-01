import { computed, ref } from "vue";

export const dashboardSections = [
  { id: "overview", label: "基本信息", group: "总览", hint: "查看用户、域名状态、证书到期时间和部署服务器" },
  { id: "activity", label: "运行概览", group: "总览", hint: "查看证书有效期、续期覆盖和任务执行情况" },
  { id: "projects", label: "项目与成员", group: "证书与部署", hint: "查看有权访问的项目并管理项目成员角色" },
  { id: "domains", label: "域名配置", group: "证书与部署", hint: "管理域名、申请证书和执行部署" },
  { id: "servers", label: "部署服务器", group: "证书与部署", hint: "配置部署目标及 SSH 认证方式" },
  { id: "dns-accounts", label: "DNS 账户管理", group: "证书与部署", hint: "查看已配置的 DNS 账号及归属用户，凭据标识脱敏显示" },
  { id: "users", label: "用户管理", group: "系统管理", hint: "管理登录账号、角色与使用状态" },
];

export function useDashboardNavigation(currentUser) {
  const selectedSection = ref("overview");
  const sections = computed(() => dashboardSections.filter(
    (section) => !["users", "dns-accounts"].includes(section.id) || ["super_admin", "admin"].includes(currentUser.value?.role),
  ));
  const activeSection = computed(() => sections.value.some(
    (section) => section.id === selectedSection.value,
  ) ? selectedSection.value : "overview");
  const activeLabel = computed(() => sections.value.find(
    (section) => section.id === activeSection.value,
  )?.label || dashboardSections[0].label);

  function selectSection(id) {
    if (sections.value.some((section) => section.id === id)) {
      selectedSection.value = id;
    }
  }

  return { activeSection, activeLabel, sections, selectSection };
}
