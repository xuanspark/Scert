import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { baseParse } from "@vue/compiler-dom";
import { parse } from "@vue/compiler-sfc";

function template(name) {
  const source = readFileSync(new URL(name, import.meta.url), "utf8");
  return baseParse(parse(source).descriptor.template.content);
}

function find(node, predicate) {
  if (predicate(node)) return node;
  for (const child of node.children || []) {
    const result = find(child, predicate);
    if (result) return result;
  }
}

function hasClass(name) {
  return (node) => node.props?.some((prop) => prop.name === "class"
    && prop.value?.content.split(/\s+/).includes(name));
}

test("certificate root remains editable during bootstrap", () => {
  const root = template("./SetupWizard.vue");
  const input = find(root, (node) => node.props?.some((prop) =>
    prop.name === "id" && prop.value?.content === "certificate-root"));
  assert.ok(input);
  assert.equal(input.props.some((prop) => prop.name === "readonly"
    || (prop.name === "bind" && prop.arg?.content === "readonly")), false);
  assert.ok(input.props.some((prop) => prop.name === "model"
    && prop.exp?.content === "form.certificate_root"));
});

test("notification step uses the system sender and keeps only recipient settings", () => {
  const root = template("./SetupWizard.vue");
  const step = find(root, hasClass("setup-notification-step"));
  const panel = find(step, hasClass("smtp-basic-panel"));
  const id = (value) => (node) => node.props?.some((prop) =>
    prop.name === "id" && prop.value?.content === value);
  assert.ok(find(step, id("email-smtp-enabled")));
  for (const value of ["email-http-enabled", "email-endpoint", "email-http-path", "email-http-auth-mode", "email-sender-name", "email-sender-email"]) {
    assert.equal(find(step, id(value)), undefined);
  }
  for (const value of ["email-smtp-host", "email-smtp-password", "recipients"]) {
    assert.ok(find(panel, id(value)), value);
  }
  const advanced = find(step, hasClass("notification-advanced-panel"));
  assert.equal(advanced.props.find((prop) => prop.name === "if").exp.content, "form.email_smtp_enabled");
  assert.ok(find(advanced, hasClass("notification-delivery-row")));
  assert.ok(find(advanced, hasClass("email-option-grid")));
  assert.equal(find(step, hasClass("reset-preview")), undefined);
  assert.ok(find(panel, hasClass("notification-recipient-row")));
});

test("notification checkboxes stay compact and centered with their labels", () => {
  const css = readFileSync(new URL("./SetupWizard.css", import.meta.url), "utf8");
  assert.doesNotMatch(css, /\.setup-notification-step input\s*[,\{]/);
  assert.match(css, /\.setup-notification-step input:not\(\[type="checkbox"\]\)/);
  const rule = (selector) => {
    const body = css.slice(css.indexOf(`${selector} {`) + selector.length + 2).split("}")[0];
    assert.ok(css.includes(`${selector} {`), selector);
    return body;
  };
  assert.match(rule(".setup-notification-step .confirm-row"), /align-items:\s*center/);
  const checkbox = rule('.setup-notification-step .confirm-row > input[type="checkbox"]');
  for (const property of ["width", "height", "min-height"]) {
    assert.match(checkbox, new RegExp(`${property}:\\s*16px`));
  }
  assert.match(checkbox, /margin:\s*0;/);
  assert.match(checkbox, /padding:\s*0;/);
  const title = rule(".setup-notification-step .confirm-row strong");
  assert.match(title, /display:\s*flex/);
  assert.match(title, /align-items:\s*center/);
});

test("account heading precedes the two-column account fields", () => {
  const root = template("./SetupWizard.vue");
  const card = find(root, (node) => node.tag === "fieldset"
    && node.children.some(hasClass("admin-account-grid")));
  const heading = card.children.findIndex(hasClass("admin-account-heading"));
  const grid = card.children.findIndex(hasClass("admin-account-grid"));
  assert.ok(heading >= 0 && heading < grid);
  assert.equal(card.children[grid].children.filter((node) => node.tag === "label").length, 4);
  assert.ok(find(card.children[heading], (node) => node.tag === "strong"));
});

test("database setup follows service access instead of occupying its grid", () => {
  const root = template("./SetupWizard.vue");
  const card = find(root, (node) => node.tag === "fieldset"
    && node.children.some(hasClass("service-access-grid")));
  const service = card.children.findIndex(hasClass("service-access-grid"));
  const database = card.children.findIndex((node) => node.tag === "DatabaseSetup");
  assert.ok(service >= 0 && database > service);
  assert.equal(find(card.children[service], (node) => node.tag === "DatabaseSetup"), undefined);
});

test("database heading precedes selection, verification and connection fields", () => {
  const section = find(template("./DatabaseSetup.vue"), hasClass("database-setup"));
  const heading = section.children.findIndex(hasClass("setup-section-heading"));
  const selection = section.children.findIndex(hasClass("database-type-field"));
  const connection = section.children.findIndex(hasClass("database-connection-grid"));
  assert.ok(heading >= 0 && selection > heading && connection > selection);
  assert.ok(find(section.children[selection], (node) => node.tag === "select"));
  assert.ok(find(section.children[selection], (node) => node.tag === "button"));
  assert.ok(find(section.children[heading], (node) => node.tag === "HelpTip"));
});

test("database connection fields retain their two-row desktop order", () => {
  const grid = find(template("./DatabaseSetup.vue"), hasClass("database-connection-grid"));
  const fields = grid.children.filter((node) => node.tag === "label").map((label) => {
    const input = find(label, (node) => node.tag === "input");
    return input.props.find((prop) => prop.name === "id").value.content;
  });
  assert.deepEqual(fields, [
    "database-host", "database-port", "database-name",
    "database-username", "database-password", "database-root-certificate",
  ]);
  const css = readFileSync(new URL("./SetupWizardLayout.css", import.meta.url), "utf8");
  const rules = [...css.matchAll(/\.access-shell--setup \.database-connection-grid\s*\{([^}]+)\}/g)];
  assert.equal(rules.length, 1);
  assert.match(rules[0][1], /grid-template-columns:\s*repeat\(3, minmax\(0, 1fr\)\)/);
});
