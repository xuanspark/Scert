// 首次初始化只提交验证令牌，不接受浏览器指定敏感凭据引用或服务端路径。
export function bootstrapPayload(legacy, databaseToken, environment) {
  const notification = {};
  for (const [key, value] of Object.entries(legacy)) {
    if (key.startsWith("email_") && key !== "email_http_credential_ref") {
      notification[key.slice(6)] = value;
    }
  }
  notification.recipients = legacy.recipients;
  return {
    database_verification_token: databaseToken,
    dns_verification_token: legacy.dns_verification_token,
    admin_username: legacy.admin_username,
    admin_email: legacy.admin_email,
    admin_password: legacy.admin_password,
    server_port: legacy.server_port,
    web_hostname: legacy.web_hostname,
    web_public_ip: legacy.web_public_ip,
    web_enabled: legacy.web_enabled,
    https_mode: legacy.https_mode,
    proxy_https_port: legacy.proxy_https_port,
    acme_environment: environment,
    propagation_timeout_secs: legacy.propagation_timeout_secs,
    propagation_polling_interval_secs: legacy.propagation_polling_interval_secs,
    notification,
  };
}
