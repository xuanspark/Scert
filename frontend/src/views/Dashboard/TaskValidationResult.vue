<script setup>
import { validationDate } from "./TaskValidationResult.js";

defineProps({ result: { type: Object, required: true } });
</script>

<template>
  <section class="task-validation" aria-label="本次证书校验结果">
    <h3>本次证书校验结果</h3>
    <dl>
      <div class="wide"><dt>覆盖域名</dt><dd>{{ result.domains.join("、") || "未记录" }}</dd></div>
      <div><dt>生效时间</dt><dd>{{ validationDate(result.not_before) }}</dd></div>
      <div><dt>到期时间</dt><dd>{{ validationDate(result.not_after) }}</dd></div>
      <div><dt>序列号</dt><dd>{{ result.serial_number || "未记录" }}</dd></div>
      <div><dt>证书链长度</dt><dd>{{ result.chain_length ?? "未记录" }}</dd></div>
      <div class="wide"><dt>SHA-256 指纹</dt><dd>{{ result.fingerprint_sha256 || "未记录" }}</dd></div>
    </dl>
    <p>此结果仅表示本次本地证书、私钥匹配及信任链校验通过，不代表远端部署或公网 HTTPS 验收通过。</p>
  </section>
</template>

<style scoped src="./TaskValidationResult.css"></style>
