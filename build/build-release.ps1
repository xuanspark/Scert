[CmdletBinding()]
param(
    [string]$BackendDirectory = "",
    [string]$OutputDirectory = "",
    [string]$DesktopDirectory = "",
    [string]$DeployDirectory = "",
    [ValidatePattern("^V[0-9]+\.[0-9]+(?:\.[0-9]+)?$")]
    [string]$Version = "V0.1.0",
    [ValidatePattern("^[0-9]{8}$")]
    [string]$ReleaseDate = (Get-Date -Format "yyyyMMdd"),
    [ValidateSet("windows-amd64", "linux-amd64")]
    [string[]]$Target = @("windows-amd64", "linux-amd64"),
    [switch]$SkipDesktop
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$scriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Definition
if ([string]::IsNullOrWhiteSpace($BackendDirectory)) {
    $BackendDirectory = Join-Path $scriptDirectory "..\backend"
}
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = Join-Path $scriptDirectory "release"
}
if ($PSBoundParameters.ContainsKey("DesktopDirectory")) {
    throw "正式发布仅包含 Web 和服务端，不再接受 DesktopDirectory；历史桌面源码请在 desktop 目录单独开发。"
}
if ($PSBoundParameters.ContainsKey("SkipDesktop")) {
    Write-Warning "SkipDesktop 仅用于兼容旧命令；正式发布始终排除桌面端，无需再传入此参数。"
}
if ([string]::IsNullOrWhiteSpace($DeployDirectory)) {
    $DeployDirectory = Join-Path $scriptDirectory "..\deploy"
}

function Resolve-SafeDirectory {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,
        [Parameter(Mandatory = $true)]
        [string]$Label
    )

    if ([string]::IsNullOrWhiteSpace($Path)) {
        throw "$Label 不能为空。"
    }
    return [System.IO.Path]::GetFullPath($Path)
}

function Invoke-GoBuild {
    param(
        [Parameter(Mandatory = $true)]
        [string]$OperatingSystem,
        [Parameter(Mandatory = $true)]
        [string]$Architecture,
        [Parameter(Mandatory = $true)]
        [string]$Destination,
        [Parameter(Mandatory = $true)]
        [string]$Package
    )

    $env:GOOS = $OperatingSystem
    $env:GOARCH = $Architecture
    $env:CGO_ENABLED = "0"
    & go build -trimpath -mod=readonly -o $Destination $Package
    if ($LASTEXITCODE -ne 0) {
        throw "构建 $OperatingSystem/$Architecture 的 $Package 失败。"
    }
}

function Write-Utf8NoBom {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,
        [Parameter(Mandatory = $true)]
        [string]$Content
    )

    [System.IO.File]::WriteAllText(
        $Path,
        $Content,
        [System.Text.UTF8Encoding]::new($false)
    )
}

function Get-RelativeReleasePath {
    param(
        [Parameter(Mandatory = $true)]
        [string]$BaseDirectory,
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $base = $BaseDirectory.TrimEnd(
        [System.IO.Path]::DirectorySeparatorChar,
        [System.IO.Path]::AltDirectorySeparatorChar
    )
    if (-not $Path.StartsWith($base + [System.IO.Path]::DirectorySeparatorChar, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "发布文件不在预期目录内：$Path"
    }
    return $Path.Substring($base.Length + 1).Replace("\", "/")
}

function Write-TargetChecksums {
    param(
        [Parameter(Mandatory = $true)]
        [string]$TargetDirectory
    )

    $checksumPath = Join-Path $TargetDirectory "SHA256SUMS"
    $lines = [System.Collections.Generic.List[string]]::new()
    Get-ChildItem -LiteralPath $TargetDirectory -Recurse -File |
        Where-Object { $_.FullName -ne $checksumPath } |
        Sort-Object FullName |
        ForEach-Object {
            $relative = Get-RelativeReleasePath -BaseDirectory $TargetDirectory -Path $_.FullName
            $hash = Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256
            $lines.Add("$($hash.Hash.ToLowerInvariant())  $relative")
        }
    Write-Utf8NoBom -Path $checksumPath -Content (($lines -join "`n") + "`n")
    return $checksumPath
}

function Invoke-ReleaseArchive {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Source,
        [Parameter(Mandatory = $true)]
        [string]$Destination,
        [Parameter(Mandatory = $true)]
        [ValidateSet("tar.gz", "zip")]
        [string]$Format,
        [string[]]$Executable = @()
    )

    $arguments = @(
        "run",
        "-trimpath",
        "-mod=readonly",
        "./cmd/scert-release",
        "--source", $Source,
        "--output", $Destination,
        "--format", $Format
    )
    foreach ($path in $Executable) {
        $arguments += @("--executable", $path)
    }

    $archiveGoOS = $env:GOOS
    $archiveGoArch = $env:GOARCH
    $archiveCGOEnabled = $env:CGO_ENABLED
    try {
        $env:GOOS = ""
        $env:GOARCH = ""
        $env:CGO_ENABLED = "0"
        & go @arguments
        if ($LASTEXITCODE -ne 0) {
            throw "生成发布归档失败：$Destination"
        }
    }
    finally {
        $env:GOOS = $archiveGoOS
        $env:GOARCH = $archiveGoArch
        $env:CGO_ENABLED = $archiveCGOEnabled
    }
}

function Test-ReleasePaths {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Source,
        [Parameter(Mandatory = $true)]
        [string[]]$Binaries,
        [Parameter(Mandatory = $true)]
        [string[]]$ForbiddenPaths
    )

    $arguments = @("run", "-trimpath", "-mod=readonly", "./cmd/scert-release", "--verify-only", "--source", $Source)
    foreach ($binary in $Binaries) {
        $arguments += @("--binary", $binary)
    }
    foreach ($path in ($ForbiddenPaths | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Select-Object -Unique)) {
        $arguments += @("--forbid-path", $path)
    }
    $checkGoOS = $env:GOOS
    $checkGoArch = $env:GOARCH
    $checkCGOEnabled = $env:CGO_ENABLED
    try {
        $env:GOOS = ""
        $env:GOARCH = ""
        $env:CGO_ENABLED = "0"
        & go @arguments
        if ($LASTEXITCODE -ne 0) {
            throw "发布路径检查未通过，停止归档。"
        }
    }
    finally {
        $env:GOOS = $checkGoOS
        $env:GOARCH = $checkGoArch
        $env:CGO_ENABLED = $checkCGOEnabled
    }
}

$backend = Resolve-SafeDirectory -Path $BackendDirectory -Label "后端目录"
$output = Resolve-SafeDirectory -Path $OutputDirectory -Label "输出目录"
$deploy = Resolve-SafeDirectory -Path $DeployDirectory -Label "部署目录"
$frontend = Resolve-SafeDirectory -Path (Join-Path $scriptDirectory "..\frontend") -Label "前端目录"

if (-not (Test-Path -LiteralPath (Join-Path $backend "go.mod") -PathType Leaf)) {
    throw "后端目录中没有找到 go.mod：$backend"
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "未找到 Go 工具链，请先安装 Go 并加入 PATH。"
}
$npmCommand = if ($env:OS -eq "Windows_NT") { "npm.cmd" } else { "npm" }
if (-not (Get-Command $npmCommand -ErrorAction SilentlyContinue)) {
    throw "未找到 npm 工具链，请先安装 Node.js 并加入 PATH。"
}
if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
    throw "未找到 Node.js 工具链，请先安装 Node.js 并加入 PATH。"
}
if (-not (Test-Path -LiteralPath (Join-Path $frontend "package.json") -PathType Leaf)) {
    throw "前端目录中没有找到 package.json：$frontend"
}
foreach ($requiredDeploymentFile in @(
    "install-linux.sh",
    "install-windows.ps1",
    "README.md",
    "systemd\Scert.service",
    "systemd\Scert-admin.service",
    "systemd\Scert-storage.service",
    "systemd\Scert-storage.socket",
    "systemd\scert-nginx.socket",
    "systemd\scert-nginx.service",
    "nginx\policy.json.example",
    "nginx\Nginx辅助服务部署说明.md",
    "systemd\Scert服务管理说明.md",
    "systemd\Scert-admin服务管理说明.md"
)) {
    $requiredPath = Join-Path $deploy $requiredDeploymentFile
    if (-not (Test-Path -LiteralPath $requiredPath -PathType Leaf)) {
        throw "部署目录缺少发布文件：$requiredPath"
    }
}

$releaseName = "Scert-$ReleaseDate-$Version"
$releaseDirectory = Join-Path $output $releaseName
if (Test-Path -LiteralPath $releaseDirectory) {
    throw "发布目录已存在，拒绝覆盖：$releaseDirectory"
}
if (@($Target | Select-Object -Unique).Count -ne $Target.Count) {
    throw "目标平台不能重复。"
}
foreach ($item in $Target) {
    $suffix = if ($item.StartsWith("linux-")) { "tar.gz" } else { "zip" }
    $archivePath = Join-Path $output "$releaseName-$item.$suffix"
    if (Test-Path -LiteralPath $archivePath) {
        throw "发布归档已存在，拒绝覆盖：$archivePath"
    }
}
$sourceCommit = (& git -C $backend rev-parse HEAD)
if ($LASTEXITCODE -ne 0) {
    throw "无法取得源码提交，不能生成可追溯发布包。"
}
$sourceChanges = @(& git -C $backend status --porcelain --untracked-files=normal)
if ($LASTEXITCODE -ne 0) {
    throw "无法检查源码工作区状态。"
}
$sourceDirty = $sourceChanges.Count -gt 0
if ($sourceDirty) {
    Write-Warning "存在未提交的源码改动，清单将标记 source_dirty=true，不能作为正式发布包。"
}

Write-Host "正在校验构建版本并按锁文件安装前端依赖……"
$toolchainJSON = & node (Join-Path $scriptDirectory "toolchain.mjs") --backend $backend --frontend $frontend --install
if ($LASTEXITCODE -ne 0) {
    throw "构建版本或依赖安装检查失败，尚未创建发布目录。"
}
$toolchain = ($toolchainJSON -join "`n") | ConvertFrom-Json
$toolchain | Add-Member -NotePropertyName powershell -NotePropertyValue $PSVersionTable.PSVersion.ToString()
New-Item -ItemType Directory -Path $releaseDirectory | Out-Null
$frontendDistribution = Join-Path $releaseDirectory "web-build"

Write-Host "正在构建 Scert Web 前端……"
Push-Location $frontend
try {
    & $npmCommand --ignore-scripts run build -- --outDir $frontendDistribution --emptyOutDir false
    if ($LASTEXITCODE -ne 0) {
        throw "构建 Scert Web 前端失败。"
    }
}
finally {
    Pop-Location
}
if (-not (Test-Path -LiteralPath (Join-Path $frontendDistribution "index.html") -PathType Leaf)) {
    throw "前端构建完成但没有找到 index.html：$frontendDistribution"
}

$oldGoOS = $env:GOOS
$oldGoArch = $env:GOARCH
$oldCGOEnabled = $env:CGO_ENABLED
$oldGoWork = $env:GOWORK
$env:GOWORK = "off"
$manifestArtifacts = [System.Collections.Generic.List[object]]::new()

Push-Location $backend
try {
    $goVersion = (& go version)
    if ($LASTEXITCODE -ne 0) {
        throw "无法取得实际构建使用的 Go 工具链版本。"
    }
    $goEnvironmentJSON = (& go env -json GOROOT GOPATH GOMODCACHE GOCACHE GOTMPDIR)
    if ($LASTEXITCODE -ne 0) {
        throw "无法取得本机构建路径，拒绝发布。"
    }
    $goEnvironment = ($goEnvironmentJSON -join "`n") | ConvertFrom-Json
    $forbiddenPaths = @(
        (Split-Path -Parent $scriptDirectory), $backend, $frontend, $deploy, $output,
        $env:USERPROFILE, $env:HOME, [System.IO.Path]::GetTempPath(),
        $goEnvironment.GOROOT, $goEnvironment.GOMODCACHE, $goEnvironment.GOCACHE, $goEnvironment.GOTMPDIR
    ) + $goEnvironment.GOPATH.Split([System.IO.Path]::PathSeparator)
    if ($env:OS -eq "Windows_NT") {
        $forbiddenPaths += @(
            (Split-Path -Parent (Get-Command go).Source),
            (Split-Path -Parent (Get-Command node).Source)
        )
    }
    foreach ($item in $Target) {
        $parts = $item.Split("-", 2)
        $goos = $parts[0]
        $goarch = $parts[1]
        $targetDirectory = Join-Path $releaseDirectory $item
        New-Item -ItemType Directory -Path $targetDirectory | Out-Null

        $extension = if ($goos -eq "windows") { ".exe" } else { "" }
        $scertPath = Join-Path $targetDirectory ("scert" + $extension)
        $adminPath = Join-Path $targetDirectory ("scert-admin" + $extension)
        $binaries = @(("scert" + $extension), ("scert-admin" + $extension))

        Write-Host "正在构建 $goos/$goarch 的 Scert……"
        Invoke-GoBuild -OperatingSystem $goos -Architecture $goarch -Destination $scertPath -Package "./cmd/scert"
        Invoke-GoBuild -OperatingSystem $goos -Architecture $goarch -Destination $adminPath -Package "./cmd/scert-admin"
        if ($goos -eq "linux") {
            Invoke-GoBuild -OperatingSystem $goos -Architecture $goarch -Destination (Join-Path $targetDirectory "scert-nginx") -Package "./cmd/scert-nginx"
            $binaries += "scert-nginx"
            Invoke-GoBuild -OperatingSystem $goos -Architecture $goarch -Destination (Join-Path $targetDirectory "scert-storage") -Package "./cmd/scert-storage"
            $binaries += "scert-storage"
        }

        $webPath = Join-Path $targetDirectory "web"
        New-Item -ItemType Directory -Path $webPath -Force | Out-Null
        Copy-Item -Path (Join-Path $frontendDistribution "*") -Destination $webPath -Recurse -Force

        Copy-Item -LiteralPath (Join-Path $deploy "README.md") -Destination (Join-Path $targetDirectory "部署说明.md")
        if ($goos -eq "linux") {
            Copy-Item -LiteralPath (Join-Path $deploy "install-linux.sh") -Destination (Join-Path $targetDirectory "install-linux.sh")
            Copy-Item -LiteralPath (Join-Path $deploy "systemd") -Destination (Join-Path $targetDirectory "systemd") -Recurse
            Copy-Item -LiteralPath (Join-Path $deploy "nginx") -Destination (Join-Path $targetDirectory "nginx") -Recurse
        }
        else {
            Copy-Item -LiteralPath (Join-Path $deploy "install-windows.ps1") -Destination (Join-Path $targetDirectory "install-windows.ps1")
        }

        $checkedPaths = @($forbiddenPaths | Where-Object { -not [string]::IsNullOrWhiteSpace($_) } | Select-Object -Unique)
        Test-ReleasePaths -Source $targetDirectory -Binaries $binaries -ForbiddenPaths $checkedPaths
        Write-TargetChecksums -TargetDirectory $targetDirectory | Out-Null
        foreach ($file in (Get-ChildItem -LiteralPath $targetDirectory -Recurse -File | Sort-Object FullName)) {
            $hash = Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256
            $manifestArtifacts.Add([ordered]@{
                target = $item
                type = "package_file"
                file = Get-RelativeReleasePath -BaseDirectory $releaseDirectory -Path $file.FullName
                size = $file.Length
                sha256 = $hash.Hash.ToLowerInvariant()
            })
        }

        if ($goos -eq "linux") {
            $archivePath = Join-Path $output "$releaseName-$item.tar.gz"
            Invoke-ReleaseArchive -Source $targetDirectory -Destination $archivePath -Format "tar.gz" -Executable (@("install-linux.sh") + $binaries)
        }
        else {
            $archivePath = Join-Path $output "$releaseName-$item.zip"
            Invoke-ReleaseArchive -Source $targetDirectory -Destination $archivePath -Format "zip"
        }
        $archive = Get-Item -LiteralPath $archivePath
        $archiveHash = Get-FileHash -LiteralPath $archivePath -Algorithm SHA256
        $manifestArtifacts.Add([ordered]@{
            target = $item
            type = "archive"
            file = $archive.Name
            size = $archive.Length
            sha256 = $archiveHash.Hash.ToLowerInvariant()
        })
    }
}
finally {
    Pop-Location
    $env:GOOS = $oldGoOS
    $env:GOARCH = $oldGoArch
    $env:CGO_ENABLED = $oldCGOEnabled
    $env:GOWORK = $oldGoWork
}

$verifiedToolchainJSON = & node (Join-Path $scriptDirectory "toolchain.mjs") --backend $backend --frontend $frontend
if ($LASTEXITCODE -ne 0) {
    throw "构建后的工具链复核失败，不生成发布清单。"
}
$verifiedToolchain = ($verifiedToolchainJSON -join "`n") | ConvertFrom-Json
if (($toolchain.input_sha256 | ConvertTo-Json -Compress) -ne ($verifiedToolchain.input_sha256 | ConvertTo-Json -Compress)) {
    throw "构建期间版本文件发生变化，不生成发布清单。"
}

$manifest = [ordered]@{
    product = "Scert"
    release_date = $ReleaseDate
    version = $Version
    source_commit = $sourceCommit.Trim()
    source_dirty = $sourceDirty
    go_toolchain = $goVersion.Trim()
    build_environment = $toolchain
    trimpath = $true
    local_path_check = "passed"
    targets = @($Target)
    delivery_scope = @("web", "server")
    desktop_requested = $false
    generated_at = [DateTimeOffset]::UtcNow.ToString("o")
    artifacts = $manifestArtifacts
}
$manifestPath = Join-Path $releaseDirectory "manifest.json"
Write-Utf8NoBom -Path $manifestPath -Content (($manifest | ConvertTo-Json -Depth 6) + "`n")

Write-Host "发布构建完成：$releaseDirectory"
Write-Host "校验清单：$manifestPath"
