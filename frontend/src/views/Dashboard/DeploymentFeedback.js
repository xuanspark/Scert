export function serverAuthLabel(server) {
  if (server.auth_method === "password") {
    return server.password_configured ? "SSH 密码已配置" : "SSH 密码未配置";
  }
  if (server.auth_method === "private_key") {
    return server.private_key_ref ? "SSH 私钥已配置" : "SSH 私钥未配置";
  }
  return "认证方式未确认";
}

export function deploymentSummary(report) {
  if (!report || !Array.isArray(report.results)) return "未收到有效部署结果";
  if (report.total === 0 && report.results.length === 0) return "未配置部署目标，未执行推送";
  if (report.status === "success" && report.total > 0 &&
      report.success === report.total && report.failed === 0 &&
      report.results.length === report.total &&
      report.results.every((item) => item.status === "success")) {
    return `证书部署完成：${report.success} 台成功`;
  }
  return "证书部署未全部成功，请检查各服务器结果";
}

export function deploymentStatusLabel(status) {
  return { success: "成功", failed: "失败", partial: "部分失败" }[status] || "未确认";
}
