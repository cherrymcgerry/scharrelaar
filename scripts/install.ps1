# Installs the scharrel binary for the current user.
#   irm https://get.scharrelaar.dev/install.ps1 | iex
# Until that domain exists, use the GitHub release URL directly:
#   irm https://github.com/cherrymcgerry/scharrelaar/releases/latest/download/install.ps1 | iex
$ErrorActionPreference = "Stop"

$repo = "cherrymcgerry/scharrelaar"
$version = if ($env:SCHARREL_VERSION) { $env:SCHARREL_VERSION } else { "latest" }
$binDir = if ($env:SCHARREL_BIN_DIR) { $env:SCHARREL_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "scharrelaar\bin" }

if ($version -eq "latest") {
    $release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
    $version = $release.tag_name.TrimStart("v")
}

$url = "https://github.com/$repo/releases/download/v$version/scharrelaar_${version}_windows_amd64.zip"
$tmp = Join-Path $env:TEMP "scharrelaar-install"
New-Item -ItemType Directory -Force -Path $tmp, $binDir | Out-Null
$zip = Join-Path $tmp "scharrel.zip"
Invoke-WebRequest $url -OutFile $zip
Expand-Archive $zip -DestinationPath $tmp -Force
Move-Item (Join-Path $tmp "scharrel.exe") (Join-Path $binDir "scharrel.exe") -Force
Remove-Item $tmp -Recurse -Force

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$binDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$binDir", "User")
    $env:Path = "$env:Path;$binDir"
    Write-Host "added $binDir to your user PATH (open a new terminal to use it)"
}

Write-Host "installed: $binDir\scharrel.exe (v$version)"
if (-not $env:SCHARREL_NO_SETUP) {
    & (Join-Path $binDir "scharrel.exe") setup
}
