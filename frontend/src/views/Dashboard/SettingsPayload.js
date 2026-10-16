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
    "smtp_enabled", "smtp_host", "smtp_port",
    "smtp_username", "smtp_password", "smtp_mode", "smtp_allow_plaintext_auth",
    "event_mode", "include_deployment_details",
    "include_certificate_serial", "include_management_url", "sender_name",
    "recipients", "timeout_secs", "retry_count",
    "retry_intervals_secs",
  ],
};


const tabSectionMap = {
  service: ["server", "web", "paths"],
  acme: ["acme"],
  dns: ["dns"],
  notification: ["notification"],
};

export function settingsPayload(current, targetTabOrSection) {
  const payload = {};
  let targetSections = null;
  if (targetTabOrSection) {
    targetSections = tabSectionMap[targetTabOrSection] || [targetTabOrSection];
  }
  for (const [section, fields] of Object.entries(requestFields)) {
    if (targetSections && !targetSections.includes(section)) continue;
    const source = current[section] || {};
    payload[section] = Object.fromEntries(
      fields.filter((field) => source[field] !== undefined)
        .map((field) => [field, source[field]]),
    );
  }
  if (!targetSections || targetSections.includes("dns")) {
    const dns = current.dns || {};
    payload.dns = { provider: dns.provider };
    if (dns.credential_ref && dns.verification_token) {
      payload.dns.credential_ref = dns.credential_ref;
      payload.dns.verification_token = dns.verification_token;
      payload.dns.test_domain = dns.test_domain;
    }
  }
  return payload;
}
