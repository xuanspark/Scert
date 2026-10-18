with open("frontend/src/views/Dashboard/DomainForm.js", "r", encoding="utf-8") as f:
    text = f.read()

old_code = """  function openCreate() {
    if (saving.value) return;
    version += 1;
    editingId.value = "";
    targetID = "";
    form.value = emptyForm();"""

new_code = """  function openCreate() {
    if (saving.value) return;
    version += 1;
    editingId.value = "";
    targetID = "";
    form.value = emptyForm();
    if (servers.value && servers.value.length > 0) {
      form.value.server_ids = [servers.value[0].id];
    }"""

text = text.replace(old_code, new_code)
with open("frontend/src/views/Dashboard/DomainForm.js", "w", encoding="utf-8") as f:
    f.write(text)

with open("frontend/src/views/Dashboard/DomainForm.test.js", "r", encoding="utf-8") as f:
    test_text = f.read()

# 替换断言
test_text = test_text.replace(
    '  state.openCreate();\n  assert.equal(state.editingId.value, "");\n  assert.deepEqual(state.form.value.server_ids, []);',
    '  state.openCreate();\n  assert.equal(state.editingId.value, "");\n  assert.deepEqual(state.form.value.server_ids, [firstServer.id]);'
)
test_text = test_text.replace(
    '  state.openCreate();\n  assert.equal(state.form.value.name, "");\n  assert.deepEqual(state.form.value.server_ids, []);',
    '  state.openCreate();\n  assert.equal(state.form.value.name, "");\n  assert.deepEqual(state.form.value.server_ids, [firstServer.id]);'
)

with open("frontend/src/views/Dashboard/DomainForm.test.js", "w", encoding="utf-8") as f:
    f.write(test_text)

print("Updated DomainForm.js and DomainForm.test.js")
