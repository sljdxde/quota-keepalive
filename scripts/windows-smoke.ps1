# QKA Windows 冒烟测试
#
# 用法（在 Windows 上，解压 QKA-<version>-windows-<arch>.zip 后）：
#   powershell -ExecutionPolicy Bypass -File windows-smoke.ps1 -PackageDir .\QKA-1.0.0-windows-amd64
#
# 它只做只读检查与 CLI 冒烟，不会写入真实账号，也不会调用任何供应商接口：
#   1. 包内文件完整性（QKA.exe / QKA-cli.exe / bun.exe / adapters / provider-plugins）
#   2. PE 形态（GUI vs Console 子系统）
#   3. QKA-cli.exe 无参数用法输出、doctor 诊断
#   4. 用临时配置目录跑一次 task-run 空跑，确认调度文件与锁逻辑能落地
#   5. bun.exe 版本
#
# 退出码：0 全部通过；1 有检查失败。

[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string]$PackageDir,
  [switch]$SkipTaskRun
)

$ErrorActionPreference = 'Stop'
$script:Failures = New-Object System.Collections.Generic.List[string]

function Step($name) { Write-Host "`n=== $name ===" -ForegroundColor Cyan }
function Pass($msg)   { Write-Host "  [PASS] $msg" -ForegroundColor Green }
function Fail($msg)   { Write-Host "  [FAIL] $msg" -ForegroundColor Red; $script:Failures.Add($msg) }
function Info($msg)   { Write-Host "  $msg" }

if (-not (Test-Path -LiteralPath $PackageDir)) {
  Write-Host "包目录不存在: $PackageDir" -ForegroundColor Red
  exit 1
}
$root = (Resolve-Path -LiteralPath $PackageDir).Path

# ---------------------------------------------------------------- 1. 文件完整性
Step '1. 包内文件完整性'
$required = @(
  'QKA.exe',
  'QKA-cli.exe',
  'bun.exe',
  'adapters',
  'provider-plugins',
  'README.txt'
)
foreach ($item in $required) {
  $path = Join-Path $root $item
  if (Test-Path -LiteralPath $path) {
    $size = if (Test-Path -LiteralPath $path -PathType Leaf) { (Get-Item -LiteralPath $path).Length } else { 0 }
    Pass "$item 存在（$([math]::Round($size/1MB, 1)) MB）"
  } else {
    Fail "$item 缺失"
  }
}
$adapterDirs = @(Get-ChildItem -LiteralPath (Join-Path $root 'adapters') -Directory -ErrorAction SilentlyContinue)
if ($adapterDirs.Count -ge 3) { Pass "adapters 含 $($adapterDirs.Count) 个适配器" }
else { Fail "adapters 数量异常: $($adapterDirs.Count)" }

# ---------------------------------------------------------------- 2. PE 形态
Step '2. PE 子系统（GUI 与 Console 不能混）'
function Get-Subsystem($path) {
  # DOS 头 0x3C 处的 e_lfanew → PE 头；可选头偏移 0x5C 处是 Subsystem。
  $bytes = [System.IO.File]::ReadAllBytes($path)
  $peOffset = [BitConverter]::ToInt32($bytes, 0x3C)
  if ([BitConverter]::ToUInt32($bytes, $peOffset) -ne 0x00004550) { return 'not-pe' }
  $optional = $peOffset + 24
  $magic = [BitConverter]::ToUInt16($bytes, $optional)
  # PE32+ 的可选头里 Subsystem 在 +0x44，PE32 在 +0x44 - 0x04 = +0x40 之前字段差异下仍是 +0x44。
  $subsystem = [BitConverter]::ToUInt16($bytes, $optional + 0x44)
  switch ($subsystem) {
    2 { return 'gui' }
    3 { return 'console' }
    default { return ("0x{0:x}" -f $subsystem) }
  }
}
foreach ($exe in @('QKA.exe', 'QKA-cli.exe')) {
  $path = Join-Path $root $exe
  if (-not (Test-Path -LiteralPath $path)) { continue }
  $sub = Get-Subsystem $path
  if ($exe -eq 'QKA.exe' -and $sub -eq 'gui') { Pass 'QKA.exe 是 Windows GUI 子系统' }
  elseif ($exe -eq 'QKA-cli.exe' -and $sub -eq 'console') { Pass 'QKA-cli.exe 是 Console 子系统' }
  else { Fail "$exe 子系统异常: $sub" }
}

# ---------------------------------------------------------------- 3. CLI 用法与 doctor
Step '3. QKA-cli.exe 基本可执行'
$cli = Join-Path $root 'QKA-cli.exe'
$usage = & $cli 2>&1 | Out-String
if ($usage -match '任务|task-run') {
  Pass '无参数启动打印用法（含 task-run）'
} else {
  Fail "无参数启动未打印预期用法，输出: $($usage.Substring(0, [Math]::Min(200, $usage.Length)))"
}

$doctor = & $cli doctor 2>&1 | Out-String
if ($LASTEXITCODE -eq 0) { Pass "doctor 退出码 0（输出 $($doctor.Length) 字节）" }
else { Fail "doctor 退出码 $LASTEXITCODE" }
Info ($doctor.Trim() -split "`n" | Select-Object -First 6 | Out-String).Trim()

# ---------------------------------------------------------------- 4. task-run 空跑
if (-not $SkipTaskRun) {
  Step '4. task-run 空跑（临时配置目录，不碰真实数据）'
  $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("qka-smoke-" + [Guid]::NewGuid().ToString('N').Substring(0, 8))
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    $env:QKA_CONFIG_DIR = $tmp
    $run = & $cli task-run 2>&1 | Out-String
    $code = $LASTEXITCODE
    Remove-Item Env:\QKA_CONFIG_DIR -ErrorAction SilentlyContinue
    if (Test-Path -LiteralPath (Join-Path $tmp 'qka.db')) {
      Pass 'task-run 在临时目录创建了 qka.db'
    } else {
      Fail "task-run 未创建配置库（退出码 $code）：$($run.Trim())"
    }
    Info ($run.Trim() -split "`n" | Select-Object -First 5 | Out-String).Trim()
  } finally {
    Remove-Item Env:\QKA_CONFIG_DIR -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }
}

# ---------------------------------------------------------------- 5. bun 运行时
Step '5. 内置 bun.exe'
$bun = Join-Path $root 'bun.exe'
if (Test-Path -LiteralPath $bun) {
  $version = & $bun --version 2>&1 | Out-String
  if ($LASTEXITCODE -eq 0 -and $version.Trim()) { Pass "bun.exe 可执行：$($version.Trim())" }
  else { Fail "bun.exe 无法执行（退出码 $LASTEXITCODE）" }
}

# ---------------------------------------------------------------- 汇总
Step '结果'
if ($script:Failures.Count -eq 0) {
  Write-Host '全部通过。GUI 仍需人工验收：托盘图标、主窗口五页签、WebView2 渲染、任务计划程序。' -ForegroundColor Green
  exit 0
}
Write-Host "失败 $($script:Failures.Count) 项：" -ForegroundColor Red
$script:Failures | ForEach-Object { Write-Host "  - $_" -ForegroundColor Red }
exit 1
