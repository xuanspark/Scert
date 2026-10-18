with open("frontend/src/views/Dashboard/DomainForm.test.js", "r", encoding="utf-8") as f:
    test_text = f.read()

test_text = test_text.replace(
    '  assert.equal(state.editingId.value, "");\n  assert.deepEqual(state.form.value.server_ids, []);\n  assert.equal(state.domainBindingsLoading.value, false);',
    '  assert.equal(state.editingId.value, "");\n  assert.deepEqual(state.form.value.server_ids, [firstServer.id]);\n  assert.equal(state.domainBindingsLoading.value, false);'
)

with open("frontend/src/views/Dashboard/DomainForm.test.js", "w", encoding="utf-8") as f:
    f.write(test_text)

print("Updated DomainForm.test.js line 241")
