[CmdletBinding()]
param(
    [string]$SourceDirectory = "",
    [string]$InstallDirectory = (Join-Path $env:ProgramFiles "Scert"),
    [string]$DataDirectory = (Join-Path $env:ProgramData "Scert"),
    [string]$CertificateDirectory = (Join-Path $env:ProgramData "Scert\ssl"),
    [ValidatePattern("^[A-Za-z0-9_.-]+$")]
    [string]$ServiceName = "Scert",
    [ValidatePattern("^[A-Za-z0-9_.-]+$")]
    [string]$AdminServiceName = "Scert-admin"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$scriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Definition
if ([string]::IsNullOrWhiteSpace($SourceDirectory)) {
    $SourceDirectory = $scriptDirectory
}

function Test-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Resolve-RequiredDirectory {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,
        [Parameter(Mandatory = $true)]
        [string]$Label
    )

    if ([string]::IsNullOrWhiteSpace($Path)) {
        throw "$Label 不能为空。"
    }
    $resolved = [System.IO.Path]::GetFullPath($Path)
    $root = [System.IO.Path]::GetPathRoot($resolved)
    if ($resolved -eq $root) {
        throw "$Label 不能使用磁盘根目录：$resolved"
    }
    return $resolved.TrimEnd([System.IO.Path]::DirectorySeparatorChar)
}

function Assert-NoReparsePoint {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path,
        [Parameter(Mandatory = $true)]
        [string]$Label
    )

    $resolved = [System.IO.Path]::GetFullPath($Path)
    $root = [System.IO.Path]::GetPathRoot($resolved)
    $current = $root
    $relative = $resolved.Substring($root.Length)
    foreach ($segment in $relative.Split([System.IO.Path]::DirectorySeparatorChar, [System.StringSplitOptions]::RemoveEmptyEntries)) {
        $current = Join-Path $current $segment
        if (Test-Path -LiteralPath $current) {
            $item = Get-Item -LiteralPath $current -Force
            if (($item.Attributes -band [System.IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw "$Label 不能包含重解析点或符号链接：$current"
            }
        }
    }
}

function Assert-ServiceStopped {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Name
    )

    $service = Get-Service -Name $Name -ErrorAction SilentlyContinue
    if ($null -ne $service -and $service.Status -ne [System.ServiceProcess.ServiceControllerStatus]::Stopped) {
        throw "升级或安装前必须停止 Windows 服务：$Name"
    }
}

function Assert-WindowsAmd64Executable {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    $stream = [System.IO.File]::OpenRead($Path)
    $reader = [System.IO.BinaryReader]::new($stream)
    try {
        if ($stream.Length -lt 64 -or $reader.ReadUInt16() -ne 0x5A4D) {
            throw "安装产物不是有效的 Windows PE 文件：$Path"
        }
        $stream.Position = 0x3C
        $peOffset = $reader.ReadInt32()
        if ($peOffset -lt 0 -or ($peOffset + 6) -gt $stream.Length) {
            throw "安装产物的 PE 头无效：$Path"
        }
        $stream.Position = $peOffset
        if ($reader.ReadUInt32() -ne 0x00004550 -or $reader.ReadUInt16() -ne 0x8664) {
            throw "安装产物不是 Windows amd64 可执行文件：$Path"
        }
    }
    finally {
        $reader.Dispose()
        $stream.Dispose()
    }
}

function Invoke-Sc {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments,
        [switch]$AllowFailure
    )

    $output = & sc.exe @Arguments 2>&1
    if ($LASTEXITCODE -ne 0 -and -not $AllowFailure) {
        throw "Windows 服务命令执行失败：sc.exe $($Arguments -join ' ')`n$($output -join "`n")"
    }
    return $output
}

function Test-ServiceExists {
    param([Parameter(Mandatory = $true)][string]$Name)
    & sc.exe query $Name *> $null
    return $LASTEXITCODE -eq 0
}

function Install-OrUpdateService {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string]$DisplayName,
        [Parameter(Mandatory = $true)][string]$Description,
        [Parameter(Mandatory = $true)][string]$BinaryPath,
        [Parameter(Mandatory = $true)][ValidateSet("auto", "demand")][string]$StartMode
    )

    if (Test-ServiceExists -Name $Name) {
        Invoke-Sc -Arguments @("config", $Name, "binPath=", $BinaryPath, "start=", $StartMode, "obj=", "NT AUTHORITY\LocalService", "DisplayName=", $DisplayName) | Out-Null
    }
    else {
        Invoke-Sc -Arguments @("create", $Name, "binPath=", $BinaryPath, "start=", $StartMode, "obj=", "NT AUTHORITY\LocalService", "DisplayName=", $DisplayName) | Out-Null
    }
    Invoke-Sc -Arguments @("description", $Name, $Description) | Out-Null
}

function Set-DirectoryAcl {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][ValidateSet("read", "modify")][string]$Mode
    )

    $localServicePermission = if ($Mode -eq "modify") { "(OI)(CI)(M)" } else { "(OI)(CI)(RX)" }
    & icacls.exe $Path /inheritance:r /grant:r `
        "BUILTIN\Administrators:(OI)(CI)(F)" `
        "NT AUTHORITY\SYSTEM:(OI)(CI)(F)" `
        "NT AUTHORITY\LOCAL SERVICE:$localServicePermission" *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "设置目录访问控制失败：$Path"
    }
}

if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    throw "该安装器只能在 Windows 上运行。"
}
if (-not (Test-Administrator)) {
    throw "请使用管理员权限运行 Scert Windows 安装器。"
}
if (-not (Get-Command sc.exe -ErrorAction SilentlyContinue)) {
    throw "系统中没有找到 sc.exe，无法注册 Windows 服务。"
}
if (-not (Get-Command icacls.exe -ErrorAction SilentlyContinue)) {
    throw "系统中没有找到 icacls.exe，无法设置安全权限。"
}

$source = Resolve-RequiredDirectory -Path $SourceDirectory -Label "安装产物目录"
$install = Resolve-RequiredDirectory -Path $InstallDirectory -Label "程序安装目录"
$data = Resolve-RequiredDirectory -Path $DataDirectory -Label "数据目录"
$certificate = Resolve-RequiredDirectory -Path $CertificateDirectory -Label "证书目录"

Assert-NoReparsePoint -Path $source -Label "安装产物目录"
Assert-NoReparsePoint -Path $install -Label "程序安装目录"
Assert-NoReparsePoint -Path $data -Label "数据目录"
Assert-NoReparsePoint -Path $certificate -Label "证书目录"
Assert-ServiceStopped -Name $ServiceName
Assert-ServiceStopped -Name $AdminServiceName

$sourceScert = Join-Path $source "scert.exe"
$sourceAdmin = Join-Path $source "scert-admin.exe"
if (-not (Test-Path -LiteralPath $sourceScert -PathType Leaf)) {
    throw "安装产物目录缺少 scert.exe：$sourceScert"
}
if (-not (Test-Path -LiteralPath $sourceAdmin -PathType Leaf)) {
    throw "安装产物目录缺少 scert-admin.exe：$sourceAdmin"
}
Assert-NoReparsePoint -Path $sourceScert -Label "Scert 安装产物"
Assert-NoReparsePoint -Path $sourceAdmin -Label "Scert-admin 安装产物"
Assert-WindowsAmd64Executable -Path $sourceScert
Assert-WindowsAmd64Executable -Path $sourceAdmin

$configDirectory = Join-Path $data "config"
$databaseDirectory = Join-Path $data "data"
$logDirectory = Join-Path $data "logs"
$backupDirectory = Join-Path $data "backups"
$docsDirectory = Join-Path $data "docs"
$runtimeDirectory = Join-Path $data "runtime"

foreach ($directory in @($install, $data, $configDirectory, $databaseDirectory, $logDirectory, $backupDirectory, $docsDirectory, $runtimeDirectory, $certificate)) {
    New-Item -ItemType Directory -Path $directory -Force | Out-Null
}

$installedScert = Join-Path $install "scert.exe"
$installedAdmin = Join-Path $install "scert-admin.exe"
Copy-Item -LiteralPath $sourceScert -Destination $installedScert -Force
Copy-Item -LiteralPath $sourceAdmin -Destination $installedAdmin -Force

Set-DirectoryAcl -Path $install -Mode read
foreach ($directory in @($data, $configDirectory, $databaseDirectory, $logDirectory, $backupDirectory, $runtimeDirectory, $certificate)) {
    Set-DirectoryAcl -Path $directory -Mode modify
}
Set-DirectoryAcl -Path $docsDirectory -Mode read

$configurationPath = Join-Path $configDirectory "scert.json"
$resetCodePath = Join-Path $runtimeDirectory "reset-code"
$mainBinaryPath = '"{0}" --config "{1}" serve' -f $installedScert, $configurationPath
$adminBinaryPath = '"{0}" --config "{1}" --code-output "{2}"' -f $installedAdmin, $configurationPath, $resetCodePath

Install-OrUpdateService `
    -Name $ServiceName `
    -DisplayName "Scert SSL 证书管理服务" `
    -Description "Scert SSL 证书申请、续期和部署服务" `
    -BinaryPath $mainBinaryPath `
    -StartMode auto

Install-OrUpdateService `
    -Name $AdminServiceName `
    -DisplayName "Scert-admin 临时密码重置服务" `
    -Description "仅在管理员手动启动时临时激活 Scert 密码重置入口" `
    -BinaryPath $adminBinaryPath `
    -StartMode demand

$metadata = [ordered]@{
    installed_at = [DateTimeOffset]::Now.ToString("o")
    install_directory = $install
    data_directory = $data
    config_directory = $configDirectory
    certificate_root = $certificate
    service_name = $ServiceName
    admin_service_name = $AdminServiceName
    service_account = "NT AUTHORITY\LocalService"
}
$metadataPath = Join-Path $configDirectory "installation.json"
$metadata | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath $metadataPath -Encoding UTF8

Write-Host "Scert Windows 文件安装和服务注册完成。"
Write-Host "核心程序：$installedScert"
Write-Host "密码重置程序：$installedAdmin"
Write-Host "配置目录：$configDirectory"
Write-Host "数据目录：$databaseDirectory"
Write-Host "日志目录：$logDirectory"
Write-Host "备份目录：$backupDirectory"
Write-Host "证书目录：$certificate"
Write-Host "安装记录：$metadataPath"
Write-Host "Scert 主服务已注册为自动启动，但本安装器没有启动或重启服务。"
Write-Host "Scert-admin 已注册为手动启动，本安装器不会启动它，也不会创建公网防火墙规则。"
Write-Host "首次初始化时请使用配置文件路径：$configurationPath"
