const requestFields = {
  server: ["host", "port", "https_mode", "proxy_https_port"],
  web: ["enabled", "hostname", "public_ip"],
  paths: ["certificate"],
  acme: [
    "ca", "environment", "account_email", "renewal_check_interval_days",
    "renewal_near_expiry_days", "renewal_threshold_days",
    "renewal_urgent_threshold_days", "renewal_max_retries",
    "renewal_retry_interval_mins", "propagation_timeout_secs",
    "propagation_polling_interval_secs",
  ],
  notification: [
    "http_enabled", "smtp_enabled", "smtp_host", "smtp_port",
    "smtp_username", "smtp_password", "smtp_mode", "smtp_allow_plaintext_auth",
    "http_credentials", "provider", "endpoint", "http_path", "http_login_path",
    "http_auth_mode", "event_mode", "include_deployment_details",
    "include_certificate_serial", "include_management_url", "sender_name",
    "sender_email", "recipients", "timeout_secs", "retry_count",
    "retry_intervals_secs",
  ],
};

export function settingsPayload(current) {
  const payload = {};
  for (const [section, fields] of Object.entries(requestFields)) {
    const source = current[section] || {};
    payload[section] = Object.fromEntries(
      fields.filter((field) => source[field] !== undefined)
        .map((field) => [field, source[field]]),
    );
  }
  const dns = current.dns || {};
  payload.dns = { provider: dns.provider };
  if (dns.credential_ref && dns.verification_token) {
    payload.dns.credential_ref = dns.credential_ref;
    payload.dns.verification_token = dns.verification_token;
    payload.dns.test_domain = dns.test_domain;
  }
  return payload;
}
