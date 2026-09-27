import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { arch, platform, release, version } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const exactVersion = /^\d+\.\d+\.\d+$/;
const npmCommand = process.platform === 'win32' ? 'npm.cmd' : 'npm';

function requireCondition(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

export function validateFrontend(pkg, lock, npmrc, actual) {
  for (const name of ['node', 'npm']) {
    const expected = pkg.engines?.[name];
    requireCondition(exactVersion.test(expected ?? ''), `${name} 必须固定精确版本。`);
    requireCondition(actual[name] === expected, `${name} 版本不匹配，需要 ${expected}。`);
    const engine = pkg.devEngines?.[name === 'node' ? 'runtime' : 'packageManager'];
    requireCondition(
      engine?.name === name && engine.version === expected && engine.onFail === 'error',
      `${name} 的开发工具链拒绝策略不一致。`,
    );
    requireCondition(lock.packages?.['']?.engines?.[name] === expected, `${name} 锁文件版本声明不一致。`);
  }
  requireCondition(pkg.packageManager === `npm@${pkg.engines.npm}`, '包管理器声明不一致。');
  requireCondition(lock.lockfileVersion === 3, '前端必须使用第三版锁文件。');
  requireCondition(pkg.name === lock.name && pkg.version === lock.version, '前端锁文件项目标识不一致。');

  for (const section of ['dependencies', 'devDependencies']) {
    const declared = pkg[section] ?? {};
    const locked = lock.packages[''][section] ?? {};
    requireCondition(
      JSON.stringify(Object.keys(declared).sort()) === JSON.stringify(Object.keys(locked).sort()),
      `前端 ${section} 与锁文件不一致。`,
    );
    for (const [name, expected] of Object.entries(declared)) {
      requireCondition(exactVersion.test(expected), `依赖 ${name} 必须固定精确版本。`);
      requireCondition(
        locked[name] === expected && lock.packages[`node_modules/${name}`]?.version === expected,
        `依赖 ${name} 与锁文件不一致。`,
      );
    }
  }

  const settings = new Map();
  for (const line of npmrc.split(/\r?\n/)) {
    const value = line.trim();
    if (!value || value.startsWith('#') || value.startsWith(';')) continue;
    const match = /^([a-z-]+)=(.*)$/.exec(value);
    requireCondition(match && !settings.has(match[1]), '前端 npm 配置包含无效项或重复项。');
    settings.set(match[1], match[2]);
  }
  for (const name of ['engine-strict', 'ignore-scripts', 'save-exact']) {
    requireCondition(settings.get(name) === 'true', `前端 npm 配置必须启用 ${name}。`);
  }
  requireCondition(settings.size === 3, '前端 npm 配置存在未审查的额外设置。');
  requireCondition(!pkg.allowScripts && !pkg.dangerouslyAllowAllScripts, '禁止额外放行依赖安装脚本。');
}

export function validateGoVersion(goMod, actual) {
  const expected = /^go (\d+\.\d+\.\d+)\s*$/m.exec(goMod)?.[1];
  requireCondition(expected, '后端模块必须声明精确 Go 版本。');
  requireCondition(actual === `go${expected}`, `实际 Go 工具链不匹配，需要 go${expected}。`);
  return expected;
}

export function validateEnvironment(env, host) {
  requireCondition(['win32', 'linux'].includes(host.platform) && host.arch === 'x64', '构建机仅支持 Windows/Linux amd64。');
  requireCondition(!env.ESBUILD_BINARY_PATH, '禁止使用锁文件之外的 esbuild 可执行文件。');
  requireCondition(!env.GOWORK || env.GOWORK === 'off', '正式构建禁止使用外部 Go 工作区。');
  requireCondition(!env.GOFLAGS, '正式构建禁止通过 GOFLAGS 覆盖依赖或编译策略。');
}

export function npmEnvironment(env = process.env) {
  // npm run 会把用户级白名单导出为环境变量；子项目安装却拒绝该变量。
  // 仅移除这项不适用于项目安装的继承策略，安装命令仍强制禁止全部脚本。
  return Object.fromEntries(Object.entries(env).filter(([name]) => name.toLowerCase() !== 'npm_config_allow_scripts'));
}

function execute(command, args, cwd, env = process.env) {
  const useShell = process.platform === 'win32' && command === npmCommand;
  if (useShell) {
    requireCondition(args.every((arg) => /^[a-zA-Z0-9=_./-]+$/.test(arg)), 'npm 参数含有不允许的命令解释器字符。');
  }
  const options = {
    cwd,
    env: command === npmCommand ? npmEnvironment(env) : env,
    encoding: 'utf8',
    windowsHide: true,
    // Windows 的 npm.cmd 需要命令解释器；参数仅来自本文件的固定值。
    shell: useShell,
    maxBuffer: 16 * 1024 * 1024,
  };
  const result = useShell
    ? spawnSync(`${command} ${args.join(' ')}`, options)
    : spawnSync(command, args, options);
  if (result.error || result.status !== 0) {
    throw new Error(`${command} 执行失败。\n${result.stderr ?? ''}`);
  }
  return result;
}

export function inputHashes(backend, frontend) {
  const files = {
    'backend/go.mod': join(backend, 'go.mod'),
    'backend/go.sum': join(backend, 'go.sum'),
    'frontend/package.json': join(frontend, 'package.json'),
    'frontend/package-lock.json': join(frontend, 'package-lock.json'),
    'frontend/.npmrc': join(frontend, '.npmrc'),
  };
  return Object.fromEntries(Object.entries(files).map(([name, path]) => [
    name,
    createHash('sha256').update(readFileSync(path)).digest('hex'),
  ]));
}

export function installDependencies(frontend) {
  // 不信任使用者的 npm 全局/用户设置，命令行参数显式覆盖；保留构建所需的可选平台包。
  const result = execute(npmCommand, [
    'ci', '--ignore-scripts', '--engine-strict', '--include=dev', '--include=optional',
    '--no-audit', '--no-fund',
  ], frontend);
  process.stderr.write(result.stdout);
  process.stderr.write(result.stderr);
}

export function inspectToolchain(backend, frontend, install = false) {
  const host = { platform: platform(), arch: arch(), release: release(), version: version() };
  validateEnvironment(process.env, host);
  const pkg = JSON.parse(readFileSync(join(frontend, 'package.json'), 'utf8'));
  const lock = JSON.parse(readFileSync(join(frontend, 'package-lock.json'), 'utf8'));
  const npmrc = readFileSync(join(frontend, '.npmrc'), 'utf8');
  const actual = { node: process.versions.node, npm: execute(npmCommand, ['--version'], frontend).stdout.trim() };
  validateFrontend(pkg, lock, npmrc, actual);
  const goEnv = { ...process.env, GOWORK: 'off' };
  const goVersion = execute('go', ['env', 'GOVERSION'], backend, goEnv).stdout.trim();
  validateGoVersion(readFileSync(join(backend, 'go.mod'), 'utf8'), goVersion);
  const hashes = inputHashes(backend, frontend);
  execute('go', ['mod', 'verify'], backend, goEnv);
  if (install) installDependencies(frontend);
  requireCondition(JSON.stringify(hashes) === JSON.stringify(inputHashes(backend, frontend)), '依赖安装改变了锁文件或版本策略，拒绝构建。');
  return {
    node: actual.node,
    npm: actual.npm,
    go: goVersion,
    host,
    frontend_dependencies: { ...pkg.dependencies, ...pkg.devDependencies },
    dependency_scripts: 'disabled',
    dependency_install: install ? 'npm-ci' : 'not-run',
    input_sha256: hashes,
  };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const { values } = parseArgs({
      options: {
        backend: { type: 'string' },
        frontend: { type: 'string' },
        install: { type: 'boolean', default: false },
      },
    });
    const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
    const report = inspectToolchain(
      resolve(values.backend ?? join(root, 'backend')),
      resolve(values.frontend ?? join(root, 'frontend')),
      values.install,
    );
    process.stdout.write(`${JSON.stringify(report)}\n`);
  } catch (error) {
    console.error(`构建工具链检查失败：${error.message}`);
    process.exitCode = 1;
  }
}
