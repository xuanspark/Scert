<script setup>
import HelpTip from "../components/HelpTip/HelpTip.vue";
defineProps({
  form: { type: Object, required: true },
  testing: Boolean,
  verified: Boolean,
  error: { type: String, default: "" },
});
const emit = defineEmits(["test"]);
</script>

<template>
  <section class="database-setup" aria-label="数据库存储配置">
    <div class="database-type-field">
      <label>
        <span>数据库类型</span>
        <select
          id="database-driver"
          name="database_driver"
          v-model="form.driver"
          :disabled="testing"
        >
          <option value="sqlite">SQLite（本机文件，无需额外服务）</option>
          <option value="mysql">MySQL</option>
          <option value="postgres">PostgreSQL</option>
        </select>
      </label>
      <div class="dns-test-actions">
        <button type="button" class="dns-test-button" :disabled="testing" @click="emit('test')">
          {{ testing ? "正在验证数据库，请等待（最长约 20 秒）……" : "验证数据库" }}
        </button>
        <span v-if="verified" role="status">数据库连接及写权限已验证。</span>
      </div>
    </div>
    <div class="setup-section-heading">
      <strong>
        数据库存储
        <HelpTip>
          选择一种存储方式，验证连接和写权限后继续。不会清空已有数据。
        </HelpTip>
      </strong>
    </div>
    <div v-if="form.driver !== 'sqlite'" class="setup-grid two-columns database-connection-grid">
      <label>
        <span>数据库主机</span>
        <input
          id="database-host"
          name="database_host"
          v-model.trim="form.host"
          autocomplete="off"
          placeholder="db.example.com"
        />
      </label>
      <label>
        <span>数据库端口</span>
        <input
          id="database-port"
          name="database_port"
          v-model.number="form.port"
          type="number"
          min="1"
          max="65535"
        />
      </label>
      <label>
        <span>数据库名称</span>
        <input
          id="database-name"
          name="database_name"
          v-model.trim="form.database"
          autocomplete="off"
        />
      </label>
      <label>
        <span>数据库用户名</span>
        <input
          id="database-username"
          name="database_username"
          v-model.trim="form.username"
          autocomplete="off"
        />
      </label>
      <label>
        <span>数据库密码</span>
        <input
          id="database-password"
          name="database_password"
          v-model="form.password"
          type="password"
          autocomplete="new-password"
        />
      </label>
      <label v-if="form.driver === 'postgres'">
        <span>根证书路径（服务器绝对路径，可选）</span>
        <input
          id="database-root-certificate"
          name="database_root_certificate"
          v-model.trim="form.root_certificate"
          placeholder="使用系统信任库时留空"
        />
      </label>
    </div>
    <p v-if="form.driver !== 'sqlite'">外部数据库必须启用 TLS 并校验证书和主机名，不支持跳过验证。请使用专属数据库与最小权限账号。</p>
    <p v-if="error" class="inline-error" role="alert">{{ error }}</p>
  </section>
</template>

<style scoped src="./DatabaseSetup.css"></style>
