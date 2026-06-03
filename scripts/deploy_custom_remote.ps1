[CmdletBinding()]
param(
  [string]$HostName = "154.12.60.218",
  [string]$User = "root",
  [string]$RemoteProject = "/opt/new-api",
  [string]$Image = "",
  [switch]$ApplyCatalog,
  [string]$SqlPath = "tmp\fzbl-import\fzbl-import.sql",
  [string]$DbUser = "root",
  [string]$DbName = "new-api"
)

$ErrorActionPreference = "Stop"

$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
Set-Location $RepoRoot

if (-not $env:REMOTE_SSH_PASSWORD) {
  throw "Set REMOTE_SSH_PASSWORD first. Example: `$env:REMOTE_SSH_PASSWORD='your-server-password'"
}

$tmpDir = Join-Path $RepoRoot "tmp"
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null

function Escape-ShellSingleQuote([string]$Value) {
  return $Value.Replace("'", "'\''")
}

$remoteProjectEscaped = Escape-ShellSingleQuote $RemoteProject
$imageEscaped = Escape-ShellSingleQuote $Image
$dbUserEscaped = Escape-ShellSingleQuote $DbUser
$dbNameEscaped = Escape-ShellSingleQuote $DbName

$remoteCommand = @"
set -eu
cd '$remoteProjectEscaped'

if [ -n '$imageEscaped' ]; then
  if [ -f .env ] && grep -q '^NEW_API_IMAGE=' .env; then
    sed -i "s#^NEW_API_IMAGE=.*#NEW_API_IMAGE=$imageEscaped#" .env
  else
    printf '\nNEW_API_IMAGE=%s\n' '$imageEscaped' >> .env
  fi
fi

echo 'Backing up PostgreSQL before deploy...'
docker compose exec -T postgres pg_dump -U '$dbUserEscaped' '$dbNameEscaped' > "backup-before-custom-deploy-`$(date +%F-%H%M%S).sql"

echo 'Pulling custom image and restarting...'
docker compose pull new-api
docker compose up -d
docker compose ps

echo 'Checking API status...'
curl -fsS http://127.0.0.1:3000/api/status
"@

$commandFile = Join-Path $tmpDir "deploy-remote-command.sh"
Set-Content -Path $commandFile -Value $remoteCommand -Encoding UTF8

node scripts/remote_exec.js `
  --host $HostName `
  --user $User `
  --command-file $commandFile

if ($ApplyCatalog) {
  if (-not (Test-Path $SqlPath)) {
    throw "SQL file not found: $SqlPath"
  }

  Write-Host "Applying generated catalog SQL..."
  node scripts/remote_apply_sql.js `
    --sql $SqlPath `
    --host $HostName `
    --user $User `
    --remote-project $RemoteProject `
    --db-user $DbUser `
    --db-name $DbName `
    --backup

  $restartCommand = "cd '$remoteProjectEscaped' && docker compose restart new-api && curl -fsS http://127.0.0.1:3000/api/status"
  node scripts/remote_exec.js `
    --host $HostName `
    --user $User `
    --command $restartCommand
}

Write-Host "Remote deploy finished."
