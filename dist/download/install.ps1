param(
  [Parameter(Mandatory = $true)]
  [string]$ENROLLTOKEN,

  [Parameter(Mandatory = $true)]
  [string]$ENROLLENDPOINT,

  [string]$MsiUrl = "$ENROLLENDPOINT/collectors/download/windows/graylog-collector.msi"
)

$ErrorActionPreference = "Stop"

$TempDir = Join-Path $env:TEMP "graylog-collector"
$MsiPath = Join-Path $TempDir "graylog-collector.msi"
$LogPath = Join-Path $TempDir "graylog-collector-install.log"
$ServiceName = "graylog-collector"

function Assert-Admin {
  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($identity)

  if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw "Please run this command in PowerShell as Administrator."
  }
}

function Initialize-InstallDirectory {
  New-Item -ItemType Directory -Force -Path $TempDir | Out-Null
}

function Download-Msi {
  Write-Host "Downloading Graylog Collector..."
  Write-Host "MSI URL: $MsiUrl"

  Invoke-WebRequest `
    -Uri $MsiUrl `
    -OutFile $MsiPath `
    -UseBasicParsing
}

function Install-Collector {
  Write-Host "Installing Graylog Collector..."
  Write-Host "Enrollment endpoint: $ENROLLENDPOINT"
  Write-Host "Install log: $LogPath"

  $arguments = @(
    "/i", "`"$MsiPath`"",
    "/quiet",
    "/norestart",
    "/log", "`"$LogPath`"",
    "ENROLLTOKEN=`"$ENROLLTOKEN`"",
    "ENROLLENDPOINT=`"$ENROLLENDPOINT`""
  )

  $process = Start-Process `
    -FilePath "msiexec.exe" `
    -ArgumentList $arguments `
    -Wait `
    -PassThru

  if ($process.ExitCode -ne 0) {
    throw "Graylog Collector installer failed with exit code $($process.ExitCode). See log: $LogPath"
  }
}

function Verify-Service {
  $service = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue

  if (-not $service) {
    Write-Warning "Graylog Collector installed, but service '$ServiceName' was not found."
    return
  }

  if ($service.Status -ne "Running") {
    Write-Host "Starting Graylog Collector service..."
    Start-Service -Name $ServiceName
  }

  $service = Get-Service -Name $ServiceName

  if ($service.Status -eq "Running") {
    Write-Host "Graylog Collector service is running."
  } else {
    Write-Warning "Graylog Collector service status is: $($service.Status)"
  }
}

try {
  Assert-Admin
  Initialize-InstallDirectory
  Download-Msi
  Install-Collector
  Verify-Service

  Write-Host ""
  Write-Host "Success. Waiting for this collector to appear in Graylog..."
  exit 0
}
catch {
  Write-Error $_.Exception.Message
  Write-Host ""
  Write-Host "Install log: $LogPath"
  exit 1
}
