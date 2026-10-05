package notification

import (
	"fmt"
	"html"
	"net/url"
	"strings"

	"git.pcux.cn/xuans/Scert/backend/internal/config"
	"git.pcux.cn/xuans/Scert/backend/internal/deployment"
)

type mailContent struct {
	title             string
	status            string
	statusColor       string
	statusSummary     string
	domain            string
	domains           []string
	expiresAt         string
	certificateSerial string
	managementURL     string
	problem           string
	recoveryWarning   string
	deployment        deployment.Report
	includeDeployment bool
	hasDeployment     bool
}

func buildMailSubject(title, status, domainName string) string {
	if status == "成功" {
		return fmt.Sprintf("[Scert] %s完成：%s", title, domainName)
	}
	return fmt.Sprintf("[Scert] %s%s：%s", title, status, domainName)
}

func buildMailContent(event Event, title, status, domainName string, cfg config.NotificationConfig) (string, string) {
	content := mailContent{
		title:             title,
		status:            status,
		statusColor:       mailStatusColor(status),
		statusSummary:     mailStatusSummary(event, title, status),
		recoveryWarning:   recoveryWarning(event),
		domain:            domainName,
		domains:           append([]string(nil), event.Domains...),
		deployment:        event.Deployment,
		includeDeployment: cfg.IncludeDeploymentDetails,
		hasDeployment:     hasDeployment(event.Deployment),
	}
	if event.ExpiresAt != nil && !event.ExpiresAt.IsZero() {
		content.expiresAt = event.ExpiresAt.Local().Format("2006-01-02 15:04:05")
	}
	if cfg.IncludeCertificateSerial {
		content.certificateSerial = strings.TrimSpace(event.CertificateSerial)
	}
	if cfg.IncludeManagementURL {
		content.managementURL = safeHTTPURL(event.ManagementURL)
	}
	if status != "成功" {
		content.problem = problemOverview(event)
	}
	return buildTextMail(content), buildHTMLMail(content)
}

func buildTextMail(content mailContent) string {
	lines := []string{
		"Scert 证书管理通知",
		"",
		"状态：" + content.status,
		"摘要：" + content.statusSummary,
		"",
		"任务信息：",
		"- 任务类型：" + content.title,
		"- 域名：" + content.domain,
	}
	if len(content.domains) > 0 {
		lines = append(lines, "- 证书域名："+strings.Join(content.domains, "、"))
	}
	if content.expiresAt != "" {
		lines = append(lines, "- 证书到期时间："+content.expiresAt)
	}
	if content.certificateSerial != "" {
		lines = append(lines, "- 证书序列号："+content.certificateSerial)
	}
	if content.hasDeployment {
		lines = append(lines, "", "部署概况：", "- "+deploymentSummary(content.deployment))
	}
	if content.problem != "" {
		lines = append(lines, "", "问题概述：", content.problem)
	}
	if content.recoveryWarning != "" {
		lines = append(lines, "", content.recoveryWarning)
	}
	if content.includeDeployment && len(content.deployment.Results) > 0 {
		lines = append(lines, "", "处理结果：")
		for _, result := range content.deployment.Results {
			lines = append(lines, fmt.Sprintf("- %s：%s，%s", result.Server, resultStatus(result.Status), resultSummary(result.Message)))
		}
	}
	if content.managementURL != "" {
		lines = append(lines, "", "管理后台："+content.managementURL)
	}
	if content.status != "成功" {
		lines = append(lines, "", "请登录管理后台查看完整执行记录，再决定是否重试。")
	}
	return strings.Join(lines, "\n")
}

func buildHTMLMail(content mailContent) string {
	var builder strings.Builder
	builder.WriteString(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"></head><body style="margin:0;padding:0;background:#f4f7fb;color:#172033;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',Arial,sans-serif;line-height:1.6;">`)
	builder.WriteString(`<div style="padding:32px 16px;"><div style="max-width:680px;margin:0 auto;background:#ffffff;border:1px solid #e4e9f2;border-radius:16px;overflow:hidden;">`)
	builder.WriteString(`<div style="padding:24px 28px;background:#14213d;color:#ffffff;"><div style="font-size:13px;letter-spacing:.08em;opacity:.8;">SCERT 证书管理服务</div><div style="margin-top:6px;font-size:24px;font-weight:700;">证书管理通知</div></div>`)
	builder.WriteString(`<div style="padding:28px;">`)
	builder.WriteString(`<div style="display:inline-block;padding:4px 12px;border-radius:999px;background:` + content.statusColor + `;color:#ffffff;font-size:13px;font-weight:700;">` + escapeHTML(content.status) + `</div>`)
	builder.WriteString(`<h1 style="margin:14px 0 6px;font-size:24px;line-height:1.35;color:#172033;">` + escapeHTML(content.statusSummary) + `</h1>`)
	builder.WriteString(`<p style="margin:0 0 24px;color:#667085;font-size:14px;">` + escapeHTML(content.domain) + `</p>`)

	builder.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="border-collapse:collapse;border:1px solid #e4e9f2;border-radius:10px;overflow:hidden;font-size:14px;">`)
	writeHTMLField(&builder, "任务类型", content.title)
	writeHTMLField(&builder, "域名", content.domain)
	if len(content.domains) > 0 {
		writeHTMLField(&builder, "证书域名", strings.Join(content.domains, "、"))
	}
	if content.expiresAt != "" {
		writeHTMLField(&builder, "证书到期时间", content.expiresAt)
	}
	if content.certificateSerial != "" {
		writeHTMLField(&builder, "证书序列号", content.certificateSerial)
	}
	builder.WriteString(`</table>`)

	if content.problem != "" {
		builder.WriteString(`<div style="margin-top:24px;padding:16px 18px;border-left:4px solid #d92d20;background:#fff3f1;border-radius:8px;"><div style="font-size:14px;font-weight:700;color:#b42318;">问题概述</div><div style="margin-top:6px;color:#7a271a;font-size:14px;">` + escapeHTML(content.problem) + `</div></div>`)
	}
	if content.hasDeployment {
		builder.WriteString(`<div style="margin-top:24px;"><div style="font-size:16px;font-weight:700;color:#172033;">部署概况</div><div style="margin-top:8px;padding:12px 14px;background:#f7f9fc;border-radius:8px;color:#344054;font-size:14px;">` + escapeHTML(deploymentSummary(content.deployment)) + `</div></div>`)
	}
	if content.recoveryWarning != "" {
		builder.WriteString(`<div style="margin-top:20px;padding:16px;border:1px solid #fdb022;border-radius:8px;background:#fffaeb;color:#93370d;font-weight:700;">` + escapeHTML(content.recoveryWarning) + `</div>`)
	}
	if content.includeDeployment && len(content.deployment.Results) > 0 {
		builder.WriteString(`<div style="margin-top:24px;"><div style="font-size:16px;font-weight:700;color:#172033;">处理结果</div>`)
		for _, result := range content.deployment.Results {
			color := resultColor(result.Status)
			builder.WriteString(`<div style="margin-top:8px;padding:12px 14px;border:1px solid #e4e9f2;border-radius:8px;font-size:14px;"><span style="font-weight:700;color:` + color + `;">` + escapeHTML(result.Server) + ` · ` + escapeHTML(resultStatus(result.Status)) + `</span><div style="margin-top:4px;color:#667085;">` + escapeHTML(resultSummary(result.Message)) + `</div></div>`)
		}
		builder.WriteString(`</div>`)
	}
	if content.managementURL != "" {
		builder.WriteString(`<div style="margin-top:28px;"><a href="` + escapeHTML(content.managementURL) + `" style="display:inline-block;padding:10px 18px;border-radius:8px;background:#2563eb;color:#ffffff;text-decoration:none;font-size:14px;font-weight:700;">打开管理后台</a></div>`)
	}
	builder.WriteString(`</div><div style="padding:16px 28px;background:#f8fafc;color:#98a2b3;font-size:12px;">此邮件由 Scert 自动发送，请勿直接回复。</div></div></div></body></html>`)
	return builder.String()
}

func writeHTMLField(builder *strings.Builder, label, value string) {
	builder.WriteString(`<tr><td style="width:34%;padding:10px 12px;border-bottom:1px solid #e4e9f2;background:#f8fafc;color:#667085;">` + escapeHTML(label) + `</td><td style="padding:10px 12px;border-bottom:1px solid #e4e9f2;color:#172033;word-break:break-word;">` + escapeHTML(value) + `</td></tr>`)
}

func mailStatusColor(status string) string {
	switch status {
	case "成功":
		return "#16803c"
	case "失败", "错误":
		return "#c4322b"
	default:
		return "#667085"
	}
}

func mailStatusSummary(event Event, title, status string) string {
	if event.Deployment.Total > 0 && event.Deployment.Failed > 0 {
		return fmt.Sprintf("%s未完成，共 %d 台服务器失败", title, event.Deployment.Failed)
	}
	if status == eventStatus("success") && event.Deployment.Total > 0 && event.Deployment.Success == event.Deployment.Total {
		return fmt.Sprintf("%s已完成，共 %d 台服务器成功", title, event.Deployment.Success)
	}
	if status == "成功" {
		return title + "已完成"
	}
	return title + "未完成，请查看问题概述"
}

func hasDeployment(report deployment.Report) bool {
	return report.Total > 0 || len(report.Results) > 0
}

func deploymentSummary(report deployment.Report) string {
	status := deploymentStatus(report.Status)
	return fmt.Sprintf("总计 %d 台，成功 %d 台，失败 %d 台，状态：%s", report.Total, report.Success, report.Failed, status)
}

func deploymentStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success":
		return "成功"
	case "partial":
		return "部分成功"
	case "failed", "failure":
		return "失败"
	default:
		return "未知"
	}
}

func resultStatus(value string) string {
	status := eventStatus(value)
	if status == "未知" && strings.EqualFold(strings.TrimSpace(value), "partial") {
		return "部分成功"
	}
	return status
}

func resultColor(value string) string {
	switch resultStatus(value) {
	case "成功":
		return "#16803c"
	case "失败", "错误":
		return "#c4322b"
	default:
		return "#667085"
	}
}

func problemOverview(event Event) string {
	items := make([]string, 0, 3)
	if detail := failureDetail(event.Error); detail != "" {
		if source := failureSource(event.Error); source != "" {
			detail = source + "：" + detail
		}
		items = appendUnique(items, detail)
	}
	for _, result := range event.Deployment.Results {
		if result.Status == "success" {
			continue
		}
		detail := failureDetail(result.Message)
		if detail == "" {
			continue
		}
		if result.Server != "" {
			detail = result.Server + "：" + detail
		}
		items = appendUnique(items, detail)
	}
	if len(items) == 0 {
		return "任务未完成，请登录管理后台查看详细执行记录。"
	}
	if len(items) > 2 {
		items = items[:2]
	}
	return strings.Join(items, "；")
}

func failureSource(value string) string {
	const prefix = "服务器 "
	start := strings.Index(value, prefix)
	if start < 0 {
		return ""
	}
	value = value[start+len(prefix):]
	end := strings.Index(value, " 的")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(value[:end])
}

func failureDetail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	switch {
	case strings.Contains(lower, "x509:") && strings.Contains(lower, "certificate is valid for"):
		return "HTTPS 证书域名不匹配，服务器返回的证书不包含目标域名"
	case strings.Contains(value, "HTTPS 校验失败") || strings.Contains(lower, "https verification"):
		return "HTTPS 校验失败，请检查服务器证书、域名和 SNI 配置"
	case strings.Contains(value, "恢复旧文件") && (strings.Contains(value, "未确认") || strings.Contains(value, "待核查")):
		return "旧文件已恢复，但服务或站点状态尚未确认"
	case strings.Contains(value, "部署锁"):
		return "部署锁仍被保留，请先核查服务器和站点状态，再决定是否重试"
	case strings.Contains(lower, "connection failed") || strings.Contains(value, "连接失败"):
		return "无法连接服务器，请检查网络、端口和服务状态"
	default:
		return compactText(value, 160)
	}
}

func resultSummary(value string) string {
	if detail := failureDetail(value); detail != "" {
		return detail
	}
	return "处理完成"
}

func compactText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len([]rune(value)) <= limit {
		return value
	}
	return string([]rune(value)[:limit]) + "…"
}

func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func safeHTTPURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.String()
}

func escapeHTML(value string) string {
	return html.EscapeString(value)
}
