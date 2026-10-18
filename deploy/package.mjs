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
    const outBin = path.join(stageDir, bin.name);
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
  const webDir = path.join(stageDir, "web");
  fs.mkdirSync(webDir, { recursive: true });
  fs.cpSync(frontendDist, webDir, { recursive: true });

  // Copy Deploy configurations
  if (fs.existsSync(path.join(deployDir, "README.md"))) {
    fs.copyFileSync(path.join(deployDir, "README.md"), path.join(stageDir, "部署说明.md"));
  }

  if (goos === "linux") {
    if (fs.existsSync(path.join(deployDir, "install-linux.sh"))) {
      fs.copyFileSync(path.join(deployDir, "install-linux.sh"), path.join(stageDir, "install-linux.sh"));
    }
    if (fs.existsSync(path.join(deployDir, "systemd"))) {
      fs.cpSync(path.join(deployDir, "systemd"), path.join(stageDir, "systemd"), { recursive: true });
    }
    if (fs.existsSync(path.join(deployDir, "nginx"))) {
      fs.cpSync(path.join(deployDir, "nginx"), path.join(stageDir, "nginx"), { recursive: true });
    }
  } else if (goos === "windows") {
    if (fs.existsSync(path.join(deployDir, "install-windows.ps1"))) {
      fs.copyFileSync(path.join(deployDir, "install-windows.ps1"), path.join(stageDir, "install-windows.ps1"));
    }
  }

  // Generate SHA256SUMS
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
    execFileSync("tar", ["-czf", archivePath, "-C", outDir, pkgName], { stdio: "inherit" });
  } else {
    // PowerShell Compress-Archive or tar
    try {
      execFileSync("tar", ["-a", "-c", "-f", archivePath, "-C", outDir, pkgName], { stdio: "inherit" });
    } catch {
      execFileSync("powershell", ["-Command", `Compress-Archive -Path '${stageDir}' -DestinationPath '${archivePath}' -Force`], { stdio: "inherit" });
    }
  }

  // Compute archive sha256
  const archiveHash = crypto.createHash("sha256").update(fs.readFileSync(archivePath)).digest("hex");
  fs.writeFileSync(`${archivePath}.sha256`, `${archiveHash}  ${archiveName}\n`, "utf8");
  console.log(`Finished ${archiveName} (${archiveHash})`);
}