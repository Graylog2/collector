<#
.SYNOPSIS
    Install Graylog Collector on Windows.

.DESCRIPTION
    Downloads the Collector MSI installer from the GitHub releases, verifies
    its checksum against the release manifest, and installs it silently. The installer registers and starts
    the "Graylog Collector" Windows service with the given enrollment settings.

    Run this script from an elevated (Administrator) PowerShell session.

.PARAMETER Endpoint
    Enrollment endpoint, usually the Graylog server URL.

.PARAMETER Token
    Enrollment token, provided by the Graylog server.

.PARAMETER TokenFile
    Path of a file that contains the enrollment token. Use this instead of
    -Token to keep the token out of the PowerShell history and process list.

.PARAMETER Version
    Collector version to install. Defaults to the latest release.

.PARAMETER SkipTlsVerify
    Do not verify the TLS certificate of the Graylog server. Use this when
    the server has a self-signed certificate.

.PARAMETER Help
    Show this help. The script also accepts --help, /? and /help.

.EXAMPLE
    .\install-windows.ps1 -Endpoint https://graylog.example.com -Token eyJhb...

.EXAMPLE
    .\install-windows.ps1 -Endpoint https://graylog.example.com -Token eyJhb... -Version 0.4.0

.EXAMPLE
    .\install-windows.ps1 -Endpoint https://graylog.example.com -TokenFile C:\path\to\token.txt

.EXAMPLE
    .\install-windows.ps1 -Endpoint https://graylog.example.com -Token eyJhb... -SkipTlsVerify

.EXAMPLE
    .\install-windows.ps1 --help
#>
#Requires -Version 5.1
# Without positional binding, unbound arguments such as --help land in
# $RemainingArguments instead of $Endpoint.
[CmdletBinding(PositionalBinding = $false)]
param(
    [string]$Endpoint,

    [string]$Token,

    [string]$TokenFile,

    [string]$Version,

    [switch]$SkipTlsVerify,

    [switch]$Help,

    # Catches arguments that PowerShell does not bind to a parameter, such as
    # the common help spellings --help and /?.
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$RemainingArguments
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
# Invoke-WebRequest is slow when it renders a progress bar.
$ProgressPreference = 'SilentlyContinue'

$GitHubRepo = 'Graylog2/collector'
$ManifestName = 'SHA256SUMS'
$ServiceName = 'graylog-collector'
$KeysDir = Join-Path $env:ProgramData 'Graylog\Collector\keys'

function Show-Help {
    Get-Help -Name $PSCommandPath -Detailed
}

function Assert-Arguments {
    $helpArguments = @('--help', '/?', '/help')
    if ($Help -or ($RemainingArguments | Where-Object { $_ -in $helpArguments })) {
        Show-Help
        exit 0
    }
    if ($RemainingArguments) {
        throw "Unknown argument(s): $($RemainingArguments -join ' '). Run the script with -Help to see the usage."
    }
    if (-not $Endpoint) {
        throw '-Endpoint is required. Run the script with -Help to see the usage.'
    }
    if ($TokenFile) {
        if ($Token) {
            throw 'Use either -Token or -TokenFile, not both.'
        }
        if (-not (Test-Path -Path $TokenFile -PathType Leaf)) {
            throw "Cannot read token file: $TokenFile"
        }
        $script:Token = (Get-Content -Path $TokenFile -Raw).Trim()
    }
    if (-not $Token) {
        throw '-Token or -TokenFile is required. Run the script with -Help to see the usage.'
    }
}

function Write-Step {
    param([string]$Message)
    Write-Host "==> $Message"
}

function Assert-Administrator {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'This script must run in an elevated PowerShell session (Run as Administrator).'
    }
}

# The Collector is enrolled when a signing key and certificate exist. It then
# keeps using them and ignores the enrollment token, so tell the user.
function Show-ExistingEnrollmentWarning {
    $keyExists = Test-Path -Path (Join-Path $KeysDir 'signing.key') -PathType Leaf
    $certExists = Test-Path -Path (Join-Path $KeysDir 'signing.crt') -PathType Leaf
    if (-not ($keyExists -and $certExists)) {
        return
    }
    Write-Warning @"
The Collector is already enrolled. Credentials exist in "$KeysDir".
The Collector keeps using them and ignores the enrollment token.
To enroll again, first remove the Collector in Graylog. The server rejects an
enrollment with new credentials while it still knows the Collector. Then stop
the service, remove the credentials, and run this script again:

    Stop-Service $ServiceName
    Remove-Item -Recurse -Force "$KeysDir"
"@
}

# Windows PowerShell 5.1 does not enable TLS 1.2 by default on older systems.
function Enable-Tls12 {
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
}

# A certificate validation callback that accepts every certificate, for
# Windows PowerShell 5.1. It must be compiled code: .NET runs the callback on a
# thread without a PowerShell runspace, so a script block fails there with
# "An unexpected error occurred on a send".
function Get-TrustAllCertificatesCallback {
    if (-not ('GraylogCollectorInstall.TrustAllCertificates' -as [type])) {
        Add-Type -TypeDefinition @"
using System.Net.Security;
using System.Security.Cryptography.X509Certificates;

namespace GraylogCollectorInstall
{
    public static class TrustAllCertificates
    {
        public static bool Validate(object sender, X509Certificate certificate, X509Chain chain, SslPolicyErrors errors)
        {
            return true;
        }

        public static RemoteCertificateValidationCallback Callback
        {
            get { return new RemoteCertificateValidationCallback(Validate); }
        }
    }
}
"@
    }
    return [GraylogCollectorInstall.TrustAllCertificates]::Callback
}

# Send one request to the given URL. Only the transport matters here, so any
# HTTP response counts as success. Returns $null on success and the error
# message otherwise.
function Invoke-EndpointProbe {
    param([string]$Uri, [bool]$SkipCertificateCheck)

    # DisableKeepAlive keeps the connection out of the process-wide pool. A
    # pooled connection that was opened without verification would otherwise
    # serve a later request with verification, and skip the handshake.
    $arguments = @{
        Uri              = $Uri
        Method           = 'Get'
        UseBasicParsing  = $true
        TimeoutSec       = 20
        DisableKeepAlive = $true
    }
    # Windows PowerShell 5.1 has no -SkipCertificateCheck. It needs the
    # process-wide callback, which is restored afterwards.
    $callbackChanged = $false
    $previousCallback = $null
    if ($SkipCertificateCheck) {
        if ($PSVersionTable.PSVersion.Major -ge 6) {
            $arguments.SkipCertificateCheck = $true
        } else {
            $previousCallback = [Net.ServicePointManager]::ServerCertificateValidationCallback
            [Net.ServicePointManager]::ServerCertificateValidationCallback = Get-TrustAllCertificatesCallback
            $callbackChanged = $true
        }
    }

    try {
        Invoke-WebRequest @arguments | Out-Null
        return $null
    } catch {
        # An HTTP error status still proves that the connection works.
        $response = $_.Exception.PSObject.Properties['Response']
        if ($response -and $response.Value) {
            return $null
        }
        return $_.Exception.Message
    } finally {
        if ($callbackChanged) {
            [Net.ServicePointManager]::ServerCertificateValidationCallback = $previousCallback
        }
    }
}

# Make sure the enrollment endpoint is reachable before anything is installed,
# and explain the two common mistakes: The supervisor rejects a server
# certificate that the system does not trust, such as a self-signed one. When
# a request fails with verification but succeeds without it, the certificate
# is the problem, and the user needs -SkipTlsVerify. When an http:// URL
# fails but the same host answers on https://, the URL has the wrong scheme.
# Known gap: a reverse proxy that answers plain HTTP on a TLS port with a
# 400 status counts as reachable here.
function Test-Endpoint {
    Write-Step "Checking connection to $Endpoint"
    if ($Endpoint.StartsWith('https://', [StringComparison]::OrdinalIgnoreCase)) {
        if ($SkipTlsVerify) {
            Write-Warning "TLS certificate verification is disabled for $Endpoint."
            $probeError = Invoke-EndpointProbe -Uri $Endpoint -SkipCertificateCheck $true
            if (-not $probeError) {
                return
            }
        } else {
            $probeError = Invoke-EndpointProbe -Uri $Endpoint -SkipCertificateCheck $false
            if (-not $probeError) {
                return
            }
            if (-not (Invoke-EndpointProbe -Uri $Endpoint -SkipCertificateCheck $true)) {
                throw @"
The TLS certificate of $Endpoint is not trusted by this system.
If the Graylog server uses a self-signed certificate, run this script again
with -SkipTlsVerify. The Collector then skips certificate verification for
this server.
"@
            }
        }
    } elseif ($Endpoint.StartsWith('http://', [StringComparison]::OrdinalIgnoreCase)) {
        $probeError = Invoke-EndpointProbe -Uri $Endpoint -SkipCertificateCheck $false
        if (-not $probeError) {
            return
        }
        $httpsEndpoint = 'https://' + $Endpoint.Substring('http://'.Length)
        $httpsError = Invoke-EndpointProbe -Uri $httpsEndpoint -SkipCertificateCheck $true
        if (-not $httpsError) {
            throw "Cannot connect to $Endpoint, but the server answers on $httpsEndpoint. Run this script again with that URL."
        }
        $probeError = "$probeError`nAlso tried ${httpsEndpoint}: $httpsError"
    } else {
        # Not an HTTP URL. Leave it to the supervisor to reject it.
        return
    }
    throw "Cannot connect to ${Endpoint}: $probeError"
}

# Download the release manifest and return the MSI entry. The manifest lists
# one "<sha256>  <file name>" line per release artifact.
function Get-MsiAsset {
    param([string]$RequestedVersion, [string]$DownloadDir)

    if ($RequestedVersion) {
        $manifestUrl = "https://github.com/$GitHubRepo/releases/download/$RequestedVersion/$ManifestName"
    } else {
        $manifestUrl = "https://github.com/$GitHubRepo/releases/latest/download/$ManifestName"
    }

    # Download to a file and read it back as text. This behaves the same in
    # Windows PowerShell 5.1 and PowerShell 7 for the octet-stream content
    # type that GitHub uses for release assets.
    $manifestPath = Join-Path $DownloadDir $ManifestName
    try {
        Invoke-WebRequest -Uri $manifestUrl -OutFile $manifestPath -UseBasicParsing
    } catch {
        $label = if ($RequestedVersion) { $RequestedVersion } else { 'latest' }
        throw "Release $label not found, or it has no ${ManifestName}: $($_.Exception.Message)"
    }
    $manifest = Get-Content -Path $manifestPath -Raw

    $pattern = '^(?<digest>[0-9a-fA-F]{64})\s+(?<name>graylog-collector-(?<tag>\S+)\.msi)$'
    $entry = $manifest -split "`r?`n" | Where-Object { $_ -match $pattern } | Select-Object -First 1
    if (-not $entry) {
        throw 'Release manifest has no Windows installer (.msi).'
    }
    $null = $entry -match $pattern

    return [PSCustomObject]@{
        Tag    = $Matches.tag
        Name   = $Matches.name
        Digest = $Matches.digest.ToLower()
        Url    = "https://github.com/$GitHubRepo/releases/download/$($Matches.tag)/$($Matches.name)"
    }
}

function Test-Checksum {
    param($Asset, [string]$FilePath)

    Write-Step 'Verifying sha256 checksum'
    $actual = (Get-FileHash -Path $FilePath -Algorithm SHA256).Hash.ToLower()
    if ($actual -ne $Asset.Digest) {
        throw "Checksum mismatch for $($Asset.Name) (expected $($Asset.Digest), got $actual)."
    }
}

function Install-Msi {
    param([string]$MsiPath, [string]$LogPath)

    $arguments = @(
        '/i', "`"$MsiPath`"",
        '/qn',
        '/norestart',
        '/l*v', "`"$LogPath`"",
        "ENROLLENDPOINT=`"$Endpoint`"",
        "ENROLLTOKEN=`"$Token`""
    )
    if ($SkipTlsVerify) {
        $arguments += 'ENROLLINSECURETLS=true'
    }

    # The log is kept only when the installation fails. Successful installs
    # leave no log behind, because it contains the install parameters.
    $process = Start-Process -FilePath 'msiexec.exe' -ArgumentList $arguments -Wait -PassThru
    switch ($process.ExitCode) {
        0 { Remove-Item -Path $LogPath -Force -ErrorAction SilentlyContinue; return }
        3010 {
            Remove-Item -Path $LogPath -Force -ErrorAction SilentlyContinue
            Write-Warning 'Installation finished. A reboot is required to complete it.'
            return
        }
        default { throw "msiexec failed with exit code $($process.ExitCode). See the log at $LogPath" }
    }
}

Assert-Arguments
Assert-Administrator
Show-ExistingEnrollmentWarning
Enable-Tls12
Test-Endpoint

if ($Version) {
    $Version = $Version.TrimStart('v')
}

$tempDir = Join-Path $env:TEMP "graylog-collector-install-$([guid]::NewGuid().ToString('N'))"
New-Item -ItemType Directory -Path $tempDir | Out-Null

try {
    $label = if ($Version) { $Version } else { 'latest' }
    Write-Step "Fetching release manifest for $label"
    $asset = Get-MsiAsset -RequestedVersion $Version -DownloadDir $tempDir

    $msiPath = Join-Path $tempDir $asset.Name
    $logPath = Join-Path $env:TEMP "graylog-collector-install-$($asset.Tag).log"

    Write-Step "Downloading $($asset.Url)"
    Invoke-WebRequest -Uri $asset.Url -OutFile $msiPath -UseBasicParsing

    Test-Checksum -Asset $asset -FilePath $msiPath

    Write-Step "Installing $($asset.Name)"
    Install-Msi -MsiPath $msiPath -LogPath $logPath
} finally {
    Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Step "Graylog Collector $($asset.Tag) installed"
Write-Host "Check the service status with: Get-Service $ServiceName"
