import fs from "node:fs";
import path from "node:path";
import crypto from "node:crypto";
import { execFileSync } from "node:child_process";

const rootDir = process.cwd();
const backendDir = path.join(rootDir, "backend");
const deployDir = path.join(rootDir, "deploy");
const frontendDist = path.join(rootDir, "frontend", "dist");
const outDir = path.join(rootDir, "out");

const args = process.argv.slice(2);
let targetArg = args.find(a => a.startsWith("--target="))?.replace("--target=", "");
let versionArg = args.find(a => a.startsWith("--version="))?.replace("--version=", "");

if (!versionArg) {
  const pkg = JSON.parse(fs.readFileSync(path.join(rootDir, "frontend", "package.json"), "utf8"));
  versionArg = pkg.version.startsWith("v") || pkg.version.startsWith("V") ? pkg.version : `v${pkg.version}`;
} else {
  if (!versionArg.startsWith("v") && !versionArg.startsWith("V")) {
    versionArg = `v${versionArg}`;
  }
}

const targets = targetArg ? [targetArg] : ["linux_amd64", "linux_arm64", "windows_amd64", "darwin_amd64", "darwin_arm64"];

fs.mkdirSync(outDir, { recursive: true });

for (const target of targets) {
  const [goos, goarch] = target.split("_");
  if (!goos || !goarch) continue;

  const pkgName = `Scert_${versionArg}_${goos}_${goarch}`;
  const stageDir = path.join(outDir, pkgName);
  fs.rmSync(stageDir, { recursive: true, force: true });
  fs.mkdirSync(stageDir, { recursive: true });

  // 顶层包含标准根目录 scert/
  const appDir = path.join(stageDir, "scert");
  fs.mkdirSync(appDir, { recursive: true });

  // 预创建标准运行目录
  for (const dir of ["config", "data", "log", "ssl"]) {
    fs.mkdirSync(path.join(appDir, dir), { recursive: true });
    // 添加 .gitkeep 保持目录结构完整
    fs.writeFileSync(path.join(appDir, dir, ".gitkeep"), "", "utf8");
  }

  const ext = goos === "windows" ? ".exe" : "";
  const binaries = [
    { name: `scert${ext}`, pkg: "./cmd/scert" },
    { name: `scert-admin${ext}`, pkg: "./cmd/scert-admin" }
  ];

  if (goos === "linux") {
    binaries.push({ name: "scert-nginx", pkg: "./cmd/scert-nginx" });
    binaries.push({ name: "scert-storage", pkg: "./cmd/scert-storage" });
  }

  console.log(`Building Go binaries for ${goos}/${goarch}...`);
  const ldflags = `-s -w -X 'git.pcux.cn/xuans/Scert/backend/internal/buildinfo.Version=${versionArg.replace(/^v/i, "")}'`;

  for (const bin of binaries) {
    const outBin = path.join(appDir, bin.name);
    execFileSync("go", ["build", "-trimpath", "-ldflags", ldflags, "-o", outBin, bin.pkg], {
      cwd: backendDir,
      env: {
        ...process.env,
        GOOS: goos,
        GOARCH: goarch,
        CGO_ENABLED: "0"
      },
      stdio: "inherit"
    });
  }

  // Copy Web frontend dist
  const webDir = path.join(appDir, "web");
  fs.mkdirSync(webDir, { recursive: true });
  fs.cpSync(frontendDist, webDir, { recursive: true });

  // Generate & Copy Release Notes / 更新说明.md / CHANGELOG.md
  let releaseNotes = "";
  try {
    const logOutput = execFileSync("git", ["log", "-n", "10", "--pretty=format:- %s (%h)"], { encoding: "utf8" }).trim();
    releaseNotes = `# Scert ${versionArg} 版本更新说明\n\n发布日期: ${new Date().toISOString().slice(0, 10)}\n\n### 本次版本主要修改与优化内容：\n\n${logOutput}\n`;
  } catch {
    releaseNotes = `# Scert ${versionArg} 版本更新说明\n\n- 自动化打包构建产物，支持全平台服务部署与运维。\n`;
  }
  fs.writeFileSync(path.join(appDir, "更新说明.md"), releaseNotes, "utf8");
  fs.writeFileSync(path.join(appDir, "CHANGELOG.md"), releaseNotes, "utf8");

  // Copy Deploy configurations into scert/deploy/
  const appDeployDir = path.join(appDir, "deploy");
  fs.mkdirSync(appDeployDir, { recursive: true });

  if (fs.existsSync(path.join(deployDir, "README.md"))) {
    fs.copyFileSync(path.join(deployDir, "README.md"), path.join(appDir, "部署说明.md"));
    fs.copyFileSync(path.join(deployDir, "README.md"), path.join(appDeployDir, "README.md"));
  }

  if (goos === "linux") {
    if (fs.existsSync(path.join(deployDir, "install-systemd.sh"))) {
      fs.copyFileSync(path.join(deployDir, "install-systemd.sh"), path.join(appDeployDir, "install-systemd.sh"));
    }
    if (fs.existsSync(path.join(deployDir, "update-systemd.sh"))) {
      fs.copyFileSync(path.join(deployDir, "update-systemd.sh"), path.join(appDeployDir, "update-systemd.sh"));
    }
    if (fs.existsSync(path.join(deployDir, "systemd"))) {
      fs.cpSync(path.join(deployDir, "systemd"), path.join(appDeployDir, "systemd"), { recursive: true });
    }
    if (fs.existsSync(path.join(deployDir, "nginx"))) {
      fs.cpSync(path.join(deployDir, "nginx"), path.join(appDeployDir, "nginx"), { recursive: true });
    }
  } else if (goos === "windows") {
    if (fs.existsSync(path.join(deployDir, "install-windows.ps1"))) {
      fs.copyFileSync(path.join(deployDir, "install-windows.ps1"), path.join(appDeployDir, "install-windows.ps1"));
    }
  }

  // Generate SHA256SUMS for the package contents
  const allFiles = [];
  function collect(dir, base) {
    for (const item of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, item.name);
      const rel = path.join(base, item.name).replace(/\\/g, "/");
      if (item.isDirectory()) {
        collect(full, rel);
      } else if (item.isFile() && item.name !== "SHA256SUMS") {
        const hash = crypto.createHash("sha256").update(fs.readFileSync(full)).digest("hex");
        allFiles.push(`${hash}  ${rel}`);
      }
    }
  }
  collect(stageDir, "");
  allFiles.sort();
  fs.writeFileSync(path.join(stageDir, "SHA256SUMS"), allFiles.join("\n") + "\n", "utf8");

  // Archive
  const archiveExt = goos === "windows" ? "zip" : "tar.gz";
  const archiveName = `${pkgName}.${archiveExt}`;
  const archivePath = path.join(outDir, archiveName);
  fs.rmSync(archivePath, { force: true });

  console.log(`Packaging ${archiveName}...`);
  if (archiveExt === "tar.gz") {
    execFileSync("tar", ["-czf", archivePath, "-C", stageDir, "scert", "SHA256SUMS"], { stdio: "inherit" });
  } else {
    try {
      execFileSync("tar", ["-a", "-c", "-f", archivePath, "-C", stageDir, "scert", "SHA256SUMS"], { stdio: "inherit" });
    } catch {
      execFileSync("powershell", ["-Command", `Compress-Archive -Path '${path.join(stageDir, "*")}' -DestinationPath '${archivePath}' -Force`], { stdio: "inherit" });
    }
  }

  // Compute archive sha256
  const archiveHash = crypto.createHash("sha256").update(fs.readFileSync(archivePath)).digest("hex");
  fs.writeFileSync(`${archivePath}.sha256`, `${archiveHash}  ${archiveName}\n`, "utf8");
  console.log(`Finished ${archiveName} (${archiveHash})`);
}
