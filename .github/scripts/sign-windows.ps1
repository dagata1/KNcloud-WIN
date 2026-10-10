# 用 WINDOWS_CERTIFICATE_BASE64 / WINDOWS_CERTIFICATE_PASSWORD 给文件做 Authenticode 签名。
# 没配置证书或找不到 signtool 时只告警、跳过（发布未签名文件），签名失败则报错。
# 用法：pwsh .github/scripts/sign-windows.ps1 dist\KNcloud-WIN.exe [更多文件…]
param([Parameter(Mandatory = $true, ValueFromRemainingArguments = $true)][string[]]$Files)
$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($env:WINDOWS_CERTIFICATE_BASE64) -or [string]::IsNullOrWhiteSpace($env:WINDOWS_CERTIFICATE_PASSWORD)) {
  Write-Host "::warning::Release signing secrets are not configured; publishing unsigned: $($Files -join ', ')"
  exit 0
}

$signTool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" -ErrorAction SilentlyContinue |
  Sort-Object FullName -Descending |
  Select-Object -First 1 -ExpandProperty FullName
if (-not $signTool) {
  Write-Host "::warning::signtool.exe was not found on the runner; publishing unsigned: $($Files -join ', ')"
  exit 0
}

$certificatePath = Join-Path $env:RUNNER_TEMP 'kncloud-windows-signing.pfx'
try {
  [IO.File]::WriteAllBytes($certificatePath, [Convert]::FromBase64String($env:WINDOWS_CERTIFICATE_BASE64))
  foreach ($f in $Files) {
    & $signTool sign /fd SHA256 /td SHA256 /tr http://timestamp.digicert.com `
      /f $certificatePath /p $env:WINDOWS_CERTIFICATE_PASSWORD $f
    if ($LASTEXITCODE -ne 0) { throw "signtool sign failed for $f with exit code $LASTEXITCODE." }
    & $signTool verify /pa /v $f
    if ($LASTEXITCODE -ne 0) { throw "signtool verify failed for $f with exit code $LASTEXITCODE." }
  }
}
finally {
  Remove-Item -LiteralPath $certificatePath -Force -ErrorAction SilentlyContinue
}
