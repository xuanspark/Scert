import sys

with open("frontend/src/views/Dashboard/DomainForm.js", "r", encoding="utf-8") as f:
    text = f.read()

old_open_create = """  function openCreate() {
    if (saving.value) return;
    version += 1;
    editingId.value = "";
    targetID = "";
    form.value = emptyForm();"""

new_open_create = """  function openCreate() {
    if (saving.value) return;
    version += 1;
    editingId.value = "";
    targetID = "";
    form.value = emptyForm();
    if (servers.value && servers.value.length > 0) {
      form.value.server_ids = [servers.value[0].id];
    }"""

if old_open_create in text:
    text = text.replace(old_open_create, new_open_create, 1)
    with open("frontend/src/views/Dashboard/DomainForm.js", "w", encoding="utf-8") as f:
        f.write(text)
    print("DomainForm.js updated successfully!")
else:
    print("old_open_create not matched!")
