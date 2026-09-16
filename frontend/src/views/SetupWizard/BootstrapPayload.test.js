import assert from "node:assert/strict";
import test from "node:test";
import { bootstrapPayload } from "./BootstrapPayload.js";

test("初始化只提交服务端验证令牌，通知字段保持兼容且不带凭据引用", () => {
  const payload = bootstrapPayload({
    admin_username: "owner", dns_verification_token: "test-dns-token",
    dns_credential_ref: "secret://untrusted/dns", certificate_root: "/untrusted",
    email_http_enabled: true, email_http_credential_ref: "secret://untrusted/mail",
    email_http_credentials: { token: "test-notification-token" },
    email_smtp_password: "test-notification-password", email_retry_count: 0,
    recipients: ["owner@example.test"],
  }, "test-database-token", "staging");
  assert.equal(payload.database_verification_token, "test-database-token");
  assert.equal(payload.dns_verification_token, "test-dns-token");
  assert.equal(payload.acme_environment, "staging");
  assert.equal("certificate_root" in payload, false);
  assert.equal("dns_credential_ref" in payload, false);
  assert.equal("http_credential_ref" in payload.notification, false);
  assert.deepEqual(payload.notification.http_credentials, { token: "test-notification-token" });
  assert.equal(payload.notification.smtp_password, "test-notification-password");
  assert.equal(payload.notification.retry_count, 0);
  assert.deepEqual(payload.notification.recipients, ["owner@example.test"]);
});
