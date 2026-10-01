export function serverAuthLabel(server) {
  return server.password_configured ? "SSH 密码已配置" : "SSH 密码未配置";
}

export function deploymentSummary(report) {
  if (!report || !Array.isArray(report.results)) return "未收到有效部署结果";
  if (report.status !== "success" && report.recovery_status === "success") {
    return "本次部署失败，已恢复旧文件并完成配置校验与重载，站点已恢复部署前证书";
  }
  if (report.status !== "success" && ["failed", "unconfirmed"].includes(report.recovery_status)) {
    return "本次部署失败，自动恢复未完成或站点状态未确认；请检查各服务器结果及部署锁，勿直接重试";
  }
  if (report.verification_status === "failed") {
    return "证书已推送，但站点 HTTPS 校验失败，尚未确认新证书生效；未自动回滚";
  }
  if (report.total === 0 && report.results.length === 0) return "未配置部署目标，未执行推送";
  if (report.status === "success" && report.total > 0 &&
      report.success === report.total && report.failed === 0 &&
      report.results.length === report.total &&
      report.results.every((item) => item.status === "success")) {
    if (report.verification_status === "success") {
      return `证书部署完成：${report.success} 台推送成功，站点 HTTPS 校验通过`;
    }
    return `证书部署完成：${report.success} 台成功`;
  }
  return "证书部署未全部成功，请检查各服务器结果";
}

export function deploymentStatusLabel(status) {
  return { success: "成功", failed: "失败", partial: "部分失败" }[status] || "未确认";
}
