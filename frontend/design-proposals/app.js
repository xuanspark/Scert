/**
 * Scert 首次初始化向导 - 全 4 步骤 UI 设计方案选型核心脚本
 * 支持 3 套视觉体系（Deck、Ops、Minimal）无缝切换与全 4 步无损联动
 */

let currentTheme = 'deck'; // 'deck' | 'ops' | 'minimal'
let currentStep = 1;       // 1 | 2 | 3 | 4

// 页面步骤元数据定义
const stepMetadata = {
  1: { num: '01', title: '超级管理员与基础配置', sub: '初始化向导 • 第 1 步 / 共 4 步 (账户与持久化存储)' },
  2: { num: '02', title: '服务与通知配置', sub: '初始化向导 • 第 2 步 / 共 4 步 (出站 SMTP 告警通道)' },
  3: { num: '03', title: 'DNS 授权与验证', sub: '初始化向导 • 第 3 步 / 共 4 步 (DNS-01 自动化凭据)' },
  4: { num: '04', title: '管理页面证书与部署生效', sub: '初始化向导 • 第 4 步 / 共 4 步 (证书目录与核心服务)' }
};

// 步骤切换处理
function goStep(step) {
  if (step < 1 || step > 4) return;
  currentStep = step;
  render();
}

function nextStep() {
  if (currentStep < 4) {
    currentStep++;
    render();
  } else {
    alert('恭喜！初始化向导全部 4 个步骤已演练完成，点击确认后可向后端发起完整生效请求。');
  }
}

function prevStep() {
  if (currentStep > 1) {
    currentStep--;
    render();
  }
}

// 方案切换处理
function switchTheme(theme) {
  currentTheme = theme;
  document.querySelectorAll('.nav-btn').forEach(btn => {
    if (btn.getAttribute('data-theme') === theme) {
      btn.classList.add('active');
    } else {
      btn.classList.remove('active');
    }
  });
  render();
}

// 渲染通用步骤进度条
function renderStepper() {
  const steps = [
    { num: 1, name: '超级管理员', desc: '账号 / 网络 / 存储' },
    { num: 2, name: '服务与通知', desc: 'SMTP 告警出站通道' },
    { num: 3, name: 'DNS 验证', desc: 'DNS-01 API 授权' },
    { num: 4, name: '证书与生效', desc: '证书目录 / 签发环境' }
  ];

  return `
    <nav class="step-stepper" aria-label="初始化步骤">
      ${steps.map(s => {
        let cls = '';
        let icon = `0${s.num}`;
        if (s.num === currentStep) {
          cls = 'current';
        } else if (s.num < currentStep) {
          cls = 'complete';
          icon = '✓';
        }
        return `
          <div class="step-item ${cls}" onclick="goStep(${s.num})" title="点击切换至步骤 ${s.num}">
            <div class="step-circle">${icon}</div>
            <div class="step-info">
              <strong>${s.name}</strong>
              <small>${s.desc}</small>
            </div>
          </div>
        `;
      }).join('')}
    </nav>
  `;
}
// =========================================================================
// 方案 A：卡片式分体与状态联运流 (Card-Deck Flow) 全 4 步表单
// =========================================================================

function renderDeckStep(step) {
  if (step === 1) {
    return `
      <!-- 步骤 1：超级管理员 -->
      <div class="deck-card">
        <div class="deck-header">
          <div class="deck-header-left">
            <div class="tag">01</div>
            <h3>超级管理员凭据录入</h3>
          </div>
          <span style="font-size: 11px; color: #64748b;">账号用于登录 Scert Web 与受保护运维 CLI</span>
        </div>
        <div class="form-grid-2">
          <div class="form-group">
            <label>超级管理员用户名 <span class="help-tip">如 scert-admin</span></label>
            <input type="text" value="xuans" placeholder="请输入管理员用户名">
          </div>
          <div class="form-group">
            <label>绑定安全电子邮箱 <span class="help-tip">用于证书到期前置推送</span></label>
            <div class="email-input-wrap">
              <input type="text" value="likb_blue" placeholder="邮箱前缀">
              <select>
                <option value="@163.com" selected>@163.com</option>
                <option value="@qq.com">@qq.com</option>
                <option value="@solit.cn">@solit.cn</option>
                <option value="@gmail.com">@gmail.com</option>
              </select>
            </div>
          </div>
          <div class="form-group">
            <label>管理员登录密码 <span class="help-tip">8-12 个字符</span></label>
            <input type="password" value="Scert@2026.Pass">
          </div>
          <div class="form-group">
            <label>确认管理员登录密码</label>
            <input type="password" value="Scert@2026.Pass">
          </div>
        </div>
      </div>

      <!-- 步骤 1：网络拓扑 -->
      <div class="deck-card">
        <div class="deck-header">
          <div class="deck-header-left">
            <div class="tag">02</div>
            <h3>网络与管理访问定位</h3>
          </div>
          <span style="font-size: 11px; color: #64748b;">支持通过公网 IP 或独立域名接入</span>
        </div>
        <div class="form-grid-2">
          <div class="form-group">
            <label>访问定位模式</label>
            <select>
              <option value="ip" selected>公网 IP 模式 (127.0.0.1)</option>
              <option value="hostname">域名定位模式 (如 scert.solit.cn)</option>
            </select>
          </div>
          <div class="form-group">
            <label>Scert 主服务运行端口</label>
            <input type="number" value="5200" placeholder="例如 5200 或 80">
          </div>
        </div>
      </div>

      <!-- 步骤 1：数据库配置 -->
      <div class="deck-card">
        <div class="deck-header">
          <div class="deck-header-left">
            <div class="tag">03</div>
            <h3>持久化存储引擎 (Database)</h3>
          </div>
          <span style="font-size: 11px; color: #64748b;">建议轻量环境使用 SQLite 本地免运维引擎</span>
        </div>
        <div class="db-verify-box">
          <div style="flex: 1; display: flex; align-items: center; gap: 14px;">
            <div class="form-group" style="width: 320px;">
              <label>存储驱动选择</label>
              <select>
                <option value="sqlite" selected>SQLite (本机文件嵌入，免运维内置锁)</option>
                <option value="mysql">MySQL 8.0+ (需支持 TLS 连接)</option>
                <option value="postgres">PostgreSQL 14+ (需 SSL/TLS 连接)</option>
              </select>
            </div>
            <div class="db-badge-verified">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg>
              <span>SQLite 读写权限已在线校验通过</span>
            </div>
          </div>
          <button class="btn-verify" onclick="alert('触发 POST /api/v1/bootstrap/database/test 校验：\n数据库读写锁验证正常，事务一致性通过。')">重新验证数据库</button>
        </div>
      </div>

      <!-- 步骤 1：安全收口提示 -->
      <div class="info-callout">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>
        <div>
          <strong>高安全受保护体系说明：</strong>
          生产环境已彻底移除公网 /reset HTTP 入口。若遗忘超级管理员密码，请在物理宿主机通过系统管理员终端运行 <code>scert.exe admin localreset</code>，在受保护的本地 IPC 域内安全重置。
        </div>
      </div>
    `;
  } else if (step === 2) {
    return `
      <!-- 步骤 2：服务与通知 -->
      <div class="deck-card">
        <div class="deck-header">
          <div class="deck-header-left">
            <div class="tag">01</div>
            <h3>SMTP 出站告警通知服务</h3>
          </div>
          <span style="font-size: 11px; color: #64748b;">仅作为出站邮件客户端向管理员分发证书通知</span>
        </div>
        <label class="checkbox-card" style="margin-bottom: 16px;">
          <input type="checkbox" checked id="smtp-switch">
          <span class="checkbox-label">启用 SMTP 告警通知</span>
          <span class="checkbox-desc">（证书申请成功、到期预警、部署失败时分发出站邮件）</span>
        </label>

        <div class="form-grid-2">
          <div class="form-group">
            <label>SMTP 服务器主机 <span class="help-tip">如 smtp.solit.cn</span></label>
            <input type="text" value="smtp.solit.cn" placeholder="smtp.example.com">
          </div>
          <div class="form-group">
            <label>SMTP 服务端口 <span class="help-tip">STARTTLS 默认 587，隐式 TLS 默认 465</span></label>
            <input type="number" value="587">
          </div>
          <div class="form-group">
            <label>发信用户名 <span class="help-tip">完整邮箱或系统账号</span></label>
            <input type="text" value="scert-notify@solit.cn">
          </div>
          <div class="form-group">
            <label>发信授权密码 / Token</label>
            <input type="password" value="Notify#Pass2026!">
          </div>
          <div class="form-group">
            <label>传输加密模式</label>
            <select>
              <option value="starttls" selected>STARTTLS (推荐安全模式)</option>
              <option value="implicit_tls">Implicit TLS (465端口直连加密)</option>
              <option value="plain">明文连接 (不推荐)</option>
            </select>
          </div>
          <div class="form-group">
            <label>默认接收通知人列表 <span class="help-tip">多个邮箱请使用逗号隔开</span></label>
            <input type="text" value="ops@solit.cn, likb_blue@163.com">
          </div>
        </div>
      </div>

      <!-- 步骤 2：高级通知偏好 -->
      <div class="deck-card">
        <div class="deck-header">
          <div class="deck-header-left">
            <div class="tag">02</div>
            <h3>高级通知策略与重试控制</h3>
          </div>
          <span style="font-size: 11px; color: #64748b;">保障高可用投递与敏感信息收敛</span>
        </div>
        <div class="form-grid-3">
          <div class="form-group">
            <label>通知详情模式</label>
            <select>
              <option value="detailed" selected>详细模式 (附带部署明细)</option>
              <option value="compact">精简模式 (仅发送状态概览)</option>
            </select>
          </div>
          <div class="form-group">
            <label>网络超时时间 (秒)</label>
            <input type="number" value="10">
          </div>
          <div class="form-group">
            <label>失败重试次数</label>
            <input type="number" value="3">
          </div>
        </div>
        <div style="display: flex; gap: 20px; margin-top: 14px;">
          <label style="font-size: 12px; display: flex; align-items: center; gap: 6px; cursor: pointer;">
            <input type="checkbox" checked accent-color="#1d4ed8"> 邮件包含证书序列号
          </label>
          <label style="font-size: 12px; display: flex; align-items: center; gap: 6px; cursor: pointer;">
            <input type="checkbox" checked accent-color="#1d4ed8"> 邮件包含管理控制台直达链接
          </label>
        </div>
      </div>
    `;
  } else if (step === 3) {
    return `
      <!-- 步骤 3：DNS 服务商凭据 -->
      <div class="deck-card">
        <div class="deck-header">
          <div class="deck-header-left">
            <div class="tag">01</div>
            <h3>DNS 服务商授权配置</h3>
          </div>
          <span style="font-size: 11px; color: #64748b;">支持通过 API 自动化注入 _acme-challenge TXT 记录</span>
        </div>
        <div class="form-grid-3">
          <div class="form-group">
            <label>DNS 服务商类型</label>
            <select>
              <option value="aliyun" selected>阿里云 DNS (Alibaba Cloud DNS)</option>
              <option value="tencent" disabled>腾讯云 DNSPod (适配器待开放)</option>
              <option value="cloudflare" disabled>Cloudflare (适配器待开放)</option>
            </select>
          </div>
          <div class="form-group">
            <label>权限测试验证主域名 <span class="help-tip">由该 DNS 账号托管</span></label>
            <input type="text" value="solit.cn" placeholder="例如 solit.cn">
          </div>
          <div class="form-group">
            <label>DNS 传播最大等待 (秒)</label>
            <input type="number" value="300">
          </div>
        </div>

        <div class="form-grid-2" style="margin-top: 14px;">
          <div class="form-group">
            <label>AccessKey ID <span class="help-tip">具有 AliyunDNSFullAccess 权限</span></label>
            <input type="text" value="LTAI5t7********98XQv">
          </div>
          <div class="form-group">
            <label>AccessKey Secret</label>
            <input type="password" value="pQ83n7Yd*********Kxm2">
          </div>
        </div>
      </div>

      <!-- 步骤 3：DNS 联调在线探针 -->
      <div class="deck-card">
        <div class="deck-header">
          <div class="deck-header-left">
            <div class="tag">02</div>
            <h3>DNS API 联调探针 (Live Verification)</h3>
          </div>
          <button class="btn-verify" onclick="alert('触发 POST /api/v1/bootstrap/dns/test：\n[阶段 1] 写入测试 TXT 记录成功 (耗时 420ms)\n[阶段 2] DNS 权威服务器查询命中 (耗时 580ms)\n[阶段 3] 测试 TXT 记录自动清理完成 (耗时 310ms)\n验证结果：DNS API 完整读写清理测试通过！')">
            执行在线 DNS 联调验证
          </button>
        </div>

        <div style="background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 8px; padding: 14px;">
          <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 10px;">
            <span style="font-size: 13px; font-weight: 600; color: #0f172a;">API 权限三阶段检验状态：</span>
            <span class="db-badge-verified">探针通过 • 有效期 15 分钟</span>
          </div>
          <div style="display: grid; grid-template-columns: repeat(3, 1fr); gap: 12px; font-size: 12px;">
            <div style="background: #fff; padding: 10px; border-radius: 6px; border: 1px solid #cbd5e1;">
              <span style="color: #64748b;">① TXT 临时记录创建</span>
              <div style="color: #10b981; font-weight: 600; margin-top: 4px;">✔ 注入成功 (Status 200)</div>
            </div>
            <div style="background: #fff; padding: 10px; border-radius: 6px; border: 1px solid #cbd5e1;">
              <span style="color: #64748b;">② 权威 DNS 节点解析</span>
              <div style="color: #10b981; font-weight: 600; margin-top: 4px;">✔ 权威节点秒级生效</div>
            </div>
            <div style="background: #fff; padding: 10px; border-radius: 6px; border: 1px solid #cbd5e1;">
              <span style="color: #64748b;">③ 残留记录原子清理</span>
              <div style="color: #10b981; font-weight: 600; margin-top: 4px;">✔ 临时记录清理完成</div>
            </div>
          </div>
        </div>
      </div>
    `;
  } else if (step === 4) {
    return `
      <!-- 步骤 4：管理页面证书与部署生效 -->
      <div class="deck-card">
        <div class="deck-header">
          <div class="deck-header-left">
            <div class="tag">01</div>
            <h3>证书部署根目录与环境</h3>
          </div>
          <span style="font-size: 11px; color: #64748b;">填写服务器宿主机绝对路径，系统将自动创建受保护目录</span>
        </div>
        <div class="form-grid-2">
          <div class="form-group">
            <label>证书部署根目录 (绝对路径) <span class="help-tip">必须具备最小安全读写权限</span></label>
            <input type="text" value="D:/Desktop/Scert/.runtime/dev/ssl">
          </div>
          <div class="form-group">
            <label>ACME 证书签发环境</label>
            <select>
              <option value="staging">测试环境 (Let's Encrypt Staging，供调试)</option>
              <option value="production" selected>正式环境 (Let's Encrypt Production，受公共浏览器信任)</option>
            </select>
          </div>
        </div>
      </div>

      <!-- 步骤 4：HTTPS 代理模式与管理入口 -->
      <div class="deck-card">
        <div class="deck-header">
          <div class="deck-header-left">
            <div class="tag">02</div>
            <h3>HTTPS 模式与服务入口设定</h3>
          </div>
          <span style="font-size: 11px; color: #64748b;">支持直接核心接管或前置反向代理</span>
        </div>
        <div class="form-grid-2">
          <div class="form-group">
            <label>HTTPS 架构模式</label>
            <select>
              <option value="direct" selected>默认模式：核心服务直接终止 HTTPS (推荐)</option>
              <option value="proxy">反向代理：Nginx / Caddy 终止公网 HTTPS</option>
            </select>
          </div>
          <div class="form-group">
            <label>管理控制台开启选项</label>
            <div style="padding-top: 8px;">
              <label class="checkbox-card" style="padding: 8px 12px;">
                <input type="checkbox" checked accent-color="#1d4ed8">
                <span class="checkbox-label">初始化完成后立即启用 Web 管理页面</span>
              </label>
            </div>
          </div>
        </div>
      </div>

      <!-- 步骤 4：就绪总结 -->
      <div class="info-callout" style="background: #eff6ff; border-color: #bfdbfe; color: #1e40af;">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/><polyline points="22 4 12 14.01 9 11.01"/></svg>
        <div>
          <strong>初始化已就绪：</strong>
          点击下方“完成初始化并启动服务”后，Scert 将在后台安全签发管理证书、固化数据库配置并直接进入主控制台。
        </div>
      </div>
    `;
  }
}
// =========================================================================
// 方案 B：沉浸式专业运维仪表板 (Ops-Console Split) 全 4 步表单与实时链路拓扑
// =========================================================================

function renderOpsStep(step) {
  let formContent = '';
  let topoContent = '';

  if (step === 1) {
    formContent = `
      <div class="deck-card" style="padding: 16px;">
        <div class="deck-header" style="margin-bottom: 12px;">
          <div class="deck-header-left">
            <div class="tag">SEC</div>
            <h3>管理员安全凭据</h3>
          </div>
        </div>
        <div class="form-grid-2">
          <div class="form-group"><label>管理员用户名</label><input type="text" value="xuans"></div>
          <div class="form-group"><label>绑定通知邮箱</label><input type="text" value="likb_blue@163.com"></div>
          <div class="form-group"><label>登录密码</label><input type="password" value="Scert@2026.Pass"></div>
          <div class="form-group"><label>确认密码</label><input type="password" value="Scert@2026.Pass"></div>
        </div>
      </div>

      <div class="deck-card" style="padding: 16px;">
        <div class="deck-header" style="margin-bottom: 12px;">
          <div class="deck-header-left">
            <div class="tag">NET</div>
            <h3>网络拓扑与存储核心</h3>
          </div>
        </div>
        <div class="form-grid-2">
          <div class="form-group"><label>网络接入模式</label><select><option>公网 IP (127.0.0.1)</option><option>域名定位 (FQDN)</option></select></div>
          <div class="form-group"><label>服务主端口</label><input type="number" value="5200"></div>
          <div class="form-group" style="grid-column: span 2;">
            <label>存储持久化驱动</label>
            <div style="display: flex; gap: 10px;">
              <select style="flex: 1;"><option>SQLite (本地独立锁文件 • 推荐免运维)</option><option>MySQL 8.0+</option><option>PostgreSQL 14+</option></select>
              <button class="btn-verify" style="height: 38px;" onclick="alert('存储引擎在线写入测试通过')">校验驱动</button>
            </div>
          </div>
        </div>
      </div>
    `;

    topoContent = `
      <div class="topo-title">
        <span>实时系统链路 (TOPOLOGY)</span>
        <span style="color: #34d399;">● READY</span>
      </div>
      <div class="topo-node">
        <div class="topo-node-name">INBOUND TRAFFIC</div>
        <div class="topo-node-val">HTTP/HTTPS :5200 (127.0.0.1)</div>
      </div>
      <div class="topo-arrow">▼ 协议隔离与拦截</div>
      <div class="topo-node">
        <div class="topo-node-name">SCERT CORE ENGINE</div>
        <div class="topo-node-val">Strict-Same-Origin Check</div>
      </div>
      <div class="topo-arrow">▼ 本地事务锁</div>
      <div class="topo-node">
        <div class="topo-node-name">STORAGE BACKEND</div>
        <div class="topo-node-val">scert.db (WAL 模式)</div>
      </div>
      <div style="margin-top: auto;">
        <div class="topo-check-item"><span>Same-Origin Header</span><span class="status-ok">PASS</span></div>
        <div class="topo-check-item"><span>Database Token</span><span class="status-ok">VERIFIED</span></div>
        <div class="topo-check-item"><span>DNS API Probe</span><span class="status-wait">STEP 3 PENDING</span></div>
        <div class="topo-check-item"><span>Admin Reset Mode</span><span class="status-ok">LOCAL IPC ONLY</span></div>
      </div>
    `;
  } else if (step === 2) {
    formContent = `
      <div class="deck-card" style="padding: 16px;">
        <div class="deck-header" style="margin-bottom: 12px;">
          <div class="deck-header-left">
            <div class="tag">MAIL</div>
            <h3>SMTP 出站路由通道</h3>
          </div>
        </div>
        <label class="checkbox-card" style="margin-bottom: 14px;">
          <input type="checkbox" checked>
          <span class="checkbox-label">启用 SMTP 告警分发机制</span>
        </label>
        <div class="form-grid-2">
          <div class="form-group"><label>SMTP 主机</label><input type="text" value="smtp.solit.cn"></div>
          <div class="form-group"><label>端口</label><input type="number" value="587"></div>
          <div class="form-group"><label>发信用户名</label><input type="text" value="scert-notify@solit.cn"></div>
          <div class="form-group"><label>发信授权码</label><input type="password" value="******"></div>
          <div class="form-group"><label>加密模式</label><select><option>STARTTLS (推荐)</option><option>Implicit TLS</option></select></div>
          <div class="form-group"><label>通知接收人</label><input type="text" value="ops@solit.cn, xuans@solit.cn"></div>
        </div>
      </div>

      <div class="deck-card" style="padding: 16px;">
        <div class="deck-header" style="margin-bottom: 12px;">
          <div class="deck-header-left">
            <div class="tag">OPT</div>
            <h3>高级通知策略</h3>
          </div>
        </div>
        <div class="form-grid-2">
          <div class="form-group"><label>通知内容粒度</label><select><option>详细模式 (含执行节点与证书链)</option><option>精炼模式</option></select></div>
          <div class="form-group"><label>超时控制 (秒)</label><input type="number" value="10"></div>
        </div>
      </div>
    `;

    topoContent = `
      <div class="topo-title">
        <span>出站通知拓扑 (OUTBOUND MAIL)</span>
        <span style="color: #38bdf8;">● CONFIGURED</span>
      </div>
      <div class="topo-node">
        <div class="topo-node-name">ALERT TRIGGER SOURCE</div>
        <div class="topo-node-val">Certificate Expiry / Renew Hook</div>
      </div>
      <div class="topo-arrow">▼ STARTTLS 587</div>
      <div class="topo-node">
        <div class="topo-node-name">GATEWAY HOST</div>
        <div class="topo-node-val">smtp.solit.cn</div>
      </div>
      <div class="topo-arrow">▼ 直达运维邮箱</div>
      <div class="topo-node">
        <div class="topo-node-name">RECIPIENTS</div>
        <div class="topo-node-val">ops@solit.cn (2 targets)</div>
      </div>
      <div style="margin-top: auto;">
        <div class="topo-check-item"><span>SMTP Host DNS</span><span class="status-ok">RESOLVED</span></div>
        <div class="topo-check-item"><span>STARTTLS Handshake</span><span class="status-ok">STANDBY</span></div>
      </div>
    `;
  } else if (step === 3) {
    formContent = `
      <div class="deck-card" style="padding: 16px;">
        <div class="deck-header" style="margin-bottom: 12px;">
          <div class="deck-header-left">
            <div class="tag">DNS</div>
            <h3>DNS-01 验证适配器凭据</h3>
          </div>
        </div>
        <div class="form-grid-2">
          <div class="form-group"><label>DNS 服务商</label><select><option>阿里云 DNS (Aliyun DNS)</option></select></div>
          <div class="form-group"><label>验证主域名</label><input type="text" value="solit.cn"></div>
          <div class="form-group"><label>AccessKey ID</label><input type="text" value="LTAI5t7********98XQv"></div>
          <div class="form-group"><label>AccessKey Secret</label><input type="password" value="pQ83n7Yd*********Kxm2"></div>
        </div>
        <div style="margin-top: 14px;">
          <button class="btn-verify" style="width: 100%; height: 38px;" onclick="alert('DNS API 自动化全链路读写探针验证通过！')">
            立即触发 DNS API 三阶段在线探针 (写入 / 权威验证 / 清理)
          </button>
        </div>
      </div>
    `;

    topoContent = `
      <div class="topo-title">
        <span>DNS-01 挑战拓扑 (CHALLENGE)</span>
        <span style="color: #34d399;">● READY</span>
      </div>
      <div class="topo-node">
        <div class="topo-node-name">ACME SERVER REQUEST</div>
        <div class="topo-node-val">_acme-challenge.solit.cn</div>
      </div>
      <div class="topo-arrow">▼ API 注入 TXT 记录</div>
      <div class="topo-node">
        <div class="topo-node-name">ALIYUN DNS CLOUD</div>
        <div class="topo-node-val">AliyunDNSFullAccess API</div>
      </div>
      <div class="topo-arrow">▼ 全球 DNS 权威解析节点</div>
      <div class="topo-node">
        <div class="topo-node-name">PROPAGATION STATUS</div>
        <div class="topo-node-val">300s SLA Max (Avg: 2.1s)</div>
      </div>
      <div style="margin-top: auto;">
        <div class="topo-check-item"><span>API Credentials</span><span class="status-ok">VALID</span></div>
        <div class="topo-check-item"><span>Live Probe</span><span class="status-ok">PASSED (1.3s)</span></div>
      </div>
    `;
  } else if (step === 4) {
    formContent = `
      <div class="deck-card" style="padding: 16px;">
        <div class="deck-header" style="margin-bottom: 12px;">
          <div class="deck-header-left">
            <div class="tag">SSL</div>
            <h3>证书根目录与签发引擎环境</h3>
          </div>
        </div>
        <div class="form-grid-2">
          <div class="form-group"><label>证书部署根目录</label><input type="text" value="D:/Desktop/Scert/.runtime/dev/ssl"></div>
          <div class="form-group"><label>ACME 环境</label><select><option>Let's Encrypt 生产环境 (Production)</option><option>Let's Encrypt 测试环境 (Staging)</option></select></div>
          <div class="form-group"><label>HTTPS 架构模式</label><select><option>直接模式 (核心直接监听 HTTPS)</option><option>反向代理模式</option></select></div>
          <div class="form-group"><label>Web 管理后台</label><select><option>初始化完成后立即开启 (Web Enabled)</option></select></div>
        </div>
      </div>
    `;

    topoContent = `
      <div class="topo-title">
        <span>生产证书生效链路 (CERT OPS)</span>
        <span style="color: #34d399;">● READY TO ISSUE</span>
      </div>
      <div class="topo-node">
        <div class="topo-node-name">MANAGED ROOT DIR</div>
        <div class="topo-node-val">D:/Desktop/Scert/.../ssl</div>
      </div>
      <div class="topo-arrow">▼ 自动下发受信任证书</div>
      <div class="topo-node">
        <div class="topo-node-name">PRODUCTION CA</div>
        <div class="topo-node-val">Let's Encrypt Production</div>
      </div>
      <div class="topo-arrow">▼ 绑定核心端口并启动</div>
      <div class="topo-node">
        <div class="topo-node-name">HTTPS LISTENER</div>
        <div class="topo-node-val">Direct TLS :5200 (Active)</div>
      </div>
      <div style="margin-top: auto;">
        <div class="topo-check-item"><span>Directory Access</span><span class="status-ok">WRITABLE</span></div>
        <div class="topo-check-item"><span>Bootstrap Lock</span><span class="status-ok">FINAL READY</span></div>
      </div>
    `;
  }

  return `
    <div class="ops-split">
      <div class="ops-main">
        ${formContent}
      </div>
      <div class="topo-panel">
        ${topoContent}
      </div>
    </div>
  `;
}
// =========================================================================
// 方案 C：极简轻奢分步引导台 (Minimalist Focus) 全 4 步表单
// =========================================================================

function renderMinimalStep(step) {
  if (step === 1) {
    return `
      <div class="minimal-container">
        <div class="minimal-section-title">01 / ADMIN IDENTITY (超级管理员账户)</div>
        <div class="minimal-card">
          <div class="form-grid-2">
            <div class="form-group"><label>管理员用户名</label><input type="text" value="xuans"></div>
            <div class="form-group"><label>绑定电子邮箱</label><input type="text" value="likb_blue@163.com"></div>
            <div class="form-group"><label>登录密码</label><input type="password" value="Scert@2026.Pass"></div>
            <div class="form-group"><label>重复密码验证</label><input type="password" value="Scert@2026.Pass"></div>
          </div>
        </div>

        <div class="minimal-section-title" style="margin-top: 10px;">02 / ACCESS & PERSISTENCE (访问入口与存储)</div>
        <div class="minimal-card">
          <div class="form-grid-3">
            <div class="form-group"><label>访问方式</label><input type="text" value="公网 IP (127.0.0.1)" readonly></div>
            <div class="form-group"><label>主端口</label><input type="number" value="5200"></div>
            <div class="form-group"><label>存储后端</label><select><option>SQLite 本机存储 (推荐)</option><option>MySQL</option><option>PostgreSQL</option></select></div>
          </div>
          <div style="margin-top: 14px; padding-top: 12px; border-top: 1px solid #f1f5f9; display: flex; justify-content: space-between; align-items: center;">
            <span style="font-size: 12px; color: #10b981; font-weight: 600;">✔ SQLite 存储文件读写状态验证正常</span>
            <button class="btn-verify" style="height: 32px;" onclick="alert('存储在线验证通过')">重新校验存储</button>
          </div>
        </div>
      </div>
    `;
  } else if (step === 2) {
    return `
      <div class="minimal-container">
        <div class="minimal-section-title">01 / NOTIFICATION ROUTE (告警通知路由)</div>
        <div class="minimal-card">
          <label class="checkbox-card" style="margin-bottom: 14px; border: none; background: transparent; padding: 0;">
            <input type="checkbox" checked>
            <span class="checkbox-label">启用 SMTP 出站通知通道</span>
          </label>
          <div class="form-grid-2">
            <div class="form-group"><label>SMTP 主机地址</label><input type="text" value="smtp.solit.cn"></div>
            <div class="form-group"><label>端口</label><input type="number" value="587"></div>
            <div class="form-group"><label>发信用户名</label><input type="text" value="scert-notify@solit.cn"></div>
            <div class="form-group"><label>发信授权密码</label><input type="password" value="******"></div>
            <div class="form-group"><label>传输加密</label><select><option>STARTTLS (推荐)</option><option>Implicit TLS</option></select></div>
            <div class="form-group"><label>接收人邮箱 (逗号分隔)</label><input type="text" value="ops@solit.cn, xuans@solit.cn"></div>
          </div>
        </div>
      </div>
    `;
  } else if (step === 3) {
    return `
      <div class="minimal-container">
        <div class="minimal-section-title">01 / DNS-01 API INTEGRATION (DNS-01 自动化接口)</div>
        <div class="minimal-card">
          <div class="form-grid-3">
            <div class="form-group"><label>DNS 服务提供商</label><select><option>阿里云 DNS (Aliyun DNS)</option></select></div>
            <div class="form-group"><label>验证测试域名</label><input type="text" value="solit.cn"></div>
            <div class="form-group"><label>传播最长等待 (秒)</label><input type="number" value="300"></div>
          </div>
          <div class="form-grid-2" style="margin-top: 14px;">
            <div class="form-group"><label>AccessKey ID</label><input type="text" value="LTAI5t7********98XQv"></div>
            <div class="form-group"><label>AccessKey Secret</label><input type="password" value="pQ83n7Yd*********Kxm2"></div>
          </div>
          <div style="margin-top: 14px; display: flex; justify-content: flex-end;">
            <button class="btn-verify" style="height: 36px; padding: 0 20px;" onclick="alert('DNS API 在线读写探针验证通过！')">
              执行 DNS 权限在线探针
            </button>
          </div>
        </div>
      </div>
    `;
  } else if (step === 4) {
    return `
      <div class="minimal-container">
        <div class="minimal-section-title">01 / CERTIFICATE & ENGINE FINALIZE (证书与最终部署)</div>
        <div class="minimal-card">
          <div class="form-grid-2">
            <div class="form-group"><label>证书部署根目录 (绝对路径)</label><input type="text" value="D:/Desktop/Scert/.runtime/dev/ssl"></div>
            <div class="form-group"><label>ACME 签发环境</label><select><option>正式生产环境 (Let's Encrypt Production)</option><option>测试环境 (Staging)</option></select></div>
            <div class="form-group"><label>HTTPS 服务模式</label><select><option>直接模式 (Direct TLS)</option><option>反向代理模式</option></select></div>
            <div class="form-group"><label>管理页面开关</label><select><option>初始化完成后立即启用 Web 控制台</option></select></div>
          </div>
        </div>
      </div>
    `;
  }
}

// =========================================================================
// 统一渲染调度主函数 render()
// =========================================================================

function render() {
  const frame = document.getElementById('preview-frame');
  if (!frame) return;

  const meta = stepMetadata[currentStep];

  // 1. 侧边栏
  const brandSidebar = `
    <aside class="access-brand">
      <div class="brand-top">
        <img class="logo" src="/Scert.png" alt="Scert">
        <div class="tag">SCERT 首次运行</div>
        <h1>配置证书管理服务</h1>
        <p>Scert 与 Scert-admin 共享本次配置。完成初始化后，即可通过管理页面统一管理全生命周期自动化证书。</p>
      </div>
      <div class="brand-notice">
        <strong>初始化安全屏障</strong>
        请先完成 DNS API 在线验证。提交初始化时将自动申请并校验管理页面证书。Linux 外部访问请提前放行端口。
      </div>
    </aside>
  `;

  // 2. 右侧主体
  let stepBody = '';
  if (currentTheme === 'deck') {
    stepBody = renderDeckStep(currentStep);
  } else if (currentTheme === 'ops') {
    stepBody = renderOpsStep(currentStep);
  } else if (currentTheme === 'minimal') {
    stepBody = renderMinimalStep(currentStep);
  }

  // 3. 底部操作栏
  const isLast = currentStep === 4;
  const isFirst = currentStep === 1;
  const footer = `
    <footer class="wizard-footer">
      <div class="hint">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="12" x2="12" y2="16"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>
        <span>当前步骤 ${currentStep}/4：请确认必填项均已填写或已通过预检</span>
      </div>
      <div class="actions">
        ${!isFirst ? `<button class="btn-prev" onclick="prevStep()">上一步</button>` : ''}
        <button class="btn-next ${isLast ? 'btn-submit' : ''}" onclick="nextStep()">
          ${isLast ? '完成初始化并启动服务' : '下一步'}
        </button>
      </div>
    </footer>
  `;

  // 4. 组装整个容器
  frame.innerHTML = `
    ${brandSidebar}
    <section class="content-area">
      <header class="wizard-header">
        <div class="title-group">
          <span>${meta.sub}</span>
          <h2>${meta.title}</h2>
        </div>
        <div class="badge-secure">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/></svg>
          受保护的 HTTPS 初始化 (Strict-Same-Origin)
        </div>
      </header>

      ${renderStepper()}

      <div class="wizard-body">
        ${stepBody}
      </div>

      ${footer}
    </section>
  `;
}

// 页面加载完成后自动触发初始渲染
window.addEventListener('DOMContentLoaded', () => {
  render();
});
render();