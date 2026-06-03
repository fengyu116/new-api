[CmdletBinding()]
param(
  [string]$FzblPath = "..\fzbl.txt",
  [string]$CankPath = "..\cank.txt",
  [string]$BaseUrl = "https://q.aibaotui.com",
  [string]$CreditUnitPrice = "0.05",
  [string]$OutputDir = "tmp\fzbl-import",
  [switch]$SkipTests,
  [switch]$GenerateSql,
  [switch]$Deploy,
  [string]$HostName = "154.12.60.218",
  [string]$User = "root",
  [string]$RemoteProject = "/opt/new-api",
  [string]$Image = ""
)

$ErrorActionPreference = "Stop"

$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location $RepoRoot

if (-not $SkipTests) {
  Write-Host "Running focused backend tests..."
  go test ./setting/special_pricing ./relay ./model
}

if ($GenerateSql) {
  if (-not $env:FZBL_UPSTREAM_KEY) {
    throw "Set FZBL_UPSTREAM_KEY first. Example: `$env:FZBL_UPSTREAM_KEY='sk-...'"
  }

  Write-Host "Generating catalog SQL..."
  python scripts/import_fzbl_catalog.py `
    --fzbl $FzblPath `
    --cank $CankPath `
    --base-url $BaseUrl `
    --credit-unit-price $CreditUnitPrice `
    --out-dir $OutputDir
}

if ($Deploy) {
  $deployArgs = @(
    "-NoProfile",
    "-ExecutionPolicy", "Bypass",
    "-File", "scripts/deploy_custom_remote.ps1",
    "-HostName", $HostName,
    "-User", $User,
    "-RemoteProject", $RemoteProject
  )

  if ($Image) {
    $deployArgs += @("-Image", $Image)
  }

  if ($GenerateSql) {
    $sqlPath = Join-Path $OutputDir "fzbl-import.sql"
    $deployArgs += @("-ApplyCatalog", "-SqlPath", $sqlPath)
  }

  Write-Host "Deploying remote server..."
  & pwsh @deployArgs
}

Write-Host "Done."
