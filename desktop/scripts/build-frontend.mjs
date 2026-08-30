import { execSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const scriptDirectory = dirname(fileURLToPath(import.meta.url));
const projectRoot = resolve(scriptDirectory, "../..");
const frontendDirectory = resolve(projectRoot, "frontend");
const desktopDistribution = resolve(scriptDirectory, "../frontend/dist");
const npmCommand = process.platform === "win32" ? "npm.cmd" : "npm";

execSync(`${npmCommand} run build`, {
  cwd: frontendDirectory,
  stdio: "inherit",
  shell: true,
});

const frontendDistribution = resolve(frontendDirectory, "dist");
if (!existsSync(frontendDistribution)) {
  throw new Error("前端构建没有生成 dist 目录");
}

mkdirSync(desktopDistribution, { recursive: true });
cpSync(frontendDistribution, desktopDistribution, { recursive: true, force: true });
