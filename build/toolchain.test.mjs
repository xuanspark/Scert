import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, existsSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { installDependencies, npmEnvironment, validateEnvironment, validateFrontend, validateGoVersion } from './toolchain.mjs';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const npmCommand = process.platform === 'win32' ? 'npm.cmd' : 'npm';

function fixture() {
  const frontend = join(root, 'frontend');
  const pkg = JSON.parse(readFileSync(join(frontend, 'package.json'), 'utf8'));
  return {
    pkg,
    lock: JSON.parse(readFileSync(join(frontend, 'package-lock.json'), 'utf8')),
    npmrc: readFileSync(join(frontend, '.npmrc'), 'utf8'),
    actual: { ...pkg.engines },
  };
}

function validate(value) {
  validateFrontend(value.pkg, value.lock, value.npmrc, value.actual);
}

test('仓库版本声明、锁文件和安装策略一致', () => {
  validate(fixture());
});

for (const [name, mutate] of Object.entries({
  '拒绝错误 Node 版本': (f) => { f.actual.node = '24.0.0'; },
  '拒绝错误 npm 版本': (f) => { f.actual.npm = '10.0.0'; },
  '拒绝工具链范围版本': (f) => { f.pkg.engines.node = '^24.19.0'; },
  '拒绝开发工具链警告降级': (f) => { f.pkg.devEngines.runtime.onFail = 'warn'; },
  '拒绝包管理器声明漂移': (f) => { f.pkg.packageManager = 'npm@10.0.0'; },
  '拒绝旧版锁文件': (f) => { f.lock.lockfileVersion = 2; },
  '拒绝锁文件工具链漂移': (f) => { f.lock.packages[''].engines.node = '24.0.0'; },
  '拒绝依赖范围版本': (f) => { f.pkg.dependencies.vue = '^3.5.41'; },
  '拒绝依赖锁定漂移': (f) => { f.lock.packages['node_modules/vue'].version = '3.5.0'; },
  '拒绝锁文件多余直接依赖': (f) => { f.lock.packages[''].dependencies.extra = '1.0.0'; },
  '拒绝前端标识漂移': (f) => { f.lock.name = 'other-project'; },
  '拒绝放行安装脚本': (f) => { f.npmrc = f.npmrc.replace('ignore-scripts=true', 'ignore-scripts=false'); },
  '拒绝重复安装策略': (f) => { f.npmrc += '\nignore-scripts=false\n'; },
  '拒绝关闭版本限制': (f) => { f.npmrc = f.npmrc.replace('engine-strict=true', 'engine-strict=false'); },
  '拒绝额外 npm 配置': (f) => { f.npmrc += '\nforce=true\n'; },
  '拒绝脚本白名单绕行': (f) => { f.pkg.allowScripts = { esbuild: true }; },
  '拒绝全量脚本放行': (f) => { f.pkg.dangerouslyAllowAllScripts = true; },
})) {
  test(name, () => {
    const value = fixture();
    mutate(value);
    assert.throws(() => validate(value));
  });
}

test('Go 工具链必须精确匹配模块基线', () => {
  assert.equal(validateGoVersion('module test\n\ngo 1.26.6\n', 'go1.26.6'), '1.26.6');
  for (const version of ['go1.26.2', 'go1.26.7', 'devel go1.27', '']) {
    assert.throws(() => validateGoVersion('go 1.26.6\n', version));
  }
  assert.throws(() => validateGoVersion('go 1.26\n', 'go1.26.6'));
});

test('拒绝外部构建覆盖和未支持的构建机', () => {
  const host = { platform: 'win32', arch: 'x64' };
  validateEnvironment({}, host);
  validateEnvironment({ GOWORK: 'off' }, { ...host, platform: 'linux' });
  for (const env of [{ ESBUILD_BINARY_PATH: 'external' }, { GOWORK: 'go.work' }, { GOFLAGS: '-mod=mod' }]) {
    assert.throws(() => validateEnvironment(env, host));
  }
  assert.throws(() => validateEnvironment({}, { ...host, platform: 'darwin' }));
  assert.throws(() => validateEnvironment({}, { ...host, arch: 'arm64' }));
});

test('发布入口在创建目录前检查并安装依赖，构建后复核版本文件', () => {
  const script = readFileSync(join(root, 'build', 'build-release.ps1'), 'utf8');
  const install = script.indexOf('--frontend $frontend --install');
  assert.ok(install > 0 && install < script.indexOf('New-Item -ItemType Directory -Path $releaseDirectory'));
  assert.match(script, /--ignore-scripts run build/);
  assert.match(script, /go build -trimpath -mod=readonly/);
  assert.match(script, /build_environment = \$toolchain/);
  assert.match(script, /\$verifiedToolchain\.input_sha256/);
});

test('工具链命令实际拒绝外部二进制覆盖', () => {
  const result = spawnSync(process.execPath, [join(root, 'build', 'toolchain.mjs'), '--install'], {
    cwd: root, encoding: 'utf8', windowsHide: true,
    env: { ...process.env, ESBUILD_BINARY_PATH: 'unreviewed-binary' },
  });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /禁止使用锁文件之外的 esbuild/);
  assert.equal(result.stdout, '');
});

test('嵌套 npm 安装不继承项目安装禁止的用户脚本白名单', () => {
  const original = { npm_config_allow_scripts: 'fixture', NPM_CONFIG_ALLOW_SCRIPTS: 'fixture', PATH: 'retained' };
  assert.deepEqual(npmEnvironment(original), { PATH: 'retained' });
  assert.equal(original.npm_config_allow_scripts, 'fixture');
});

test('发布脚本实际在工具链拒绝后不创建产物', { skip: process.platform !== 'win32' }, (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'scert-release-preflight-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const output = join(directory, 'release');
  const result = spawnSync('powershell.exe', [
    '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', join(root, 'build', 'build-release.ps1'),
    '-OutputDirectory', output,
  ], {
    cwd: root, encoding: 'utf8', windowsHide: true,
    env: { ...process.env, ESBUILD_BINARY_PATH: 'unreviewed-binary' },
  });
  assert.equal(result.status, 1, `${result.error ?? ''}\n${result.stdout}\n${result.stderr}`);
  assert.match(result.stderr, /esbuild/);
  assert.ok(!existsSync(output));
});

test('真实离线安装禁止根项目及依赖脚本，命令行策略覆盖环境绕行', { timeout: 120000 }, (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'scert-install-policy-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const dependency = join(directory, 'dependency');
  mkdirSync(dependency);
  const script = 'node -e "require(\'node:fs\').writeFileSync(\'lifecycle-ran\', \'yes\')"';
  writeFileSync(join(dependency, 'package.json'), JSON.stringify({
    name: 'scert-policy-fixture', version: '1.0.0', scripts: { postinstall: script },
  }));
  writeFileSync(join(directory, 'package.json'), JSON.stringify({
    name: 'scert-policy-test', version: '1.0.0', private: true,
    dependencies: { 'scert-policy-fixture': 'file:./dependency' },
    scripts: { postinstall: script },
  }));
  writeFileSync(join(directory, '.npmrc'), 'ignore-scripts=true\n');
  const run = (args) => {
    const options = { cwd: directory, encoding: 'utf8', windowsHide: true, env: npmEnvironment() };
    const result = process.platform === 'win32'
      ? spawnSync(`${npmCommand} ${args.join(' ')}`, { ...options, shell: true })
      : spawnSync(npmCommand, args, options);
    assert.equal(result.status, 0, `${result.error ?? ''}\n${result.stdout}\n${result.stderr}`);
  };
  run(['install', '--package-lock-only', '--ignore-scripts', '--offline', '--no-audit', '--no-fund']);
  const lock = readFileSync(join(directory, 'package-lock.json'));
  const envKeys = ['npm_config_ignore_scripts', 'npm_config_dangerously_allow_all_scripts', 'npm_config_offline'];
  const previous = envKeys.map((key) => process.env[key]);
  try {
    process.env.npm_config_ignore_scripts = 'false';
    process.env.npm_config_dangerously_allow_all_scripts = 'true';
    process.env.npm_config_offline = 'true';
    installDependencies(directory);
  } finally {
    envKeys.forEach((key, i) => {
      if (previous[i] === undefined) delete process.env[key];
      else process.env[key] = previous[i];
    });
  }
  assert.ok(existsSync(join(directory, 'node_modules', 'scert-policy-fixture', 'package.json')));
  assert.ok(!existsSync(join(directory, 'lifecycle-ran')));
  assert.ok(!existsSync(join(dependency, 'lifecycle-ran')));
  assert.deepEqual(readFileSync(join(directory, 'package-lock.json')), lock);
  // 正向对照证明夹具确实会执行脚本，避免用未触发的脚本证明禁止策略。
  run(['ci', '--ignore-scripts=false', '--dangerously-allow-all-scripts', '--offline', '--no-audit', '--no-fund']);
  assert.ok(existsSync(join(directory, 'lifecycle-ran')));
  assert.ok(existsSync(join(dependency, 'lifecycle-ran')));
});
