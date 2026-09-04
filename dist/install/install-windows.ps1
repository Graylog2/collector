<#
.SYNOPSIS
    Install Graylog Collector on Windows.

.DESCRIPTION
    Downloads the Collector MSI installer from the GitHub releases, verifies
    its checksum against the release manifest, and installs it silently. The installer registers and starts
    the "Graylog Collector" Windows service with the given enrollment settings.

    Run this script from an elevated (Administrator) PowerShell session.

    The script is meant for the initial installation. On a machine where the
    Collector is already installed, the installer keeps the endpoint, token,
    and TLS setting from the first installation. To change them, uninstall
    the Collector first.

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
$AuthCheckPath = '/v1/opamp-enroll-auth-check'
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

# A certificate validation callback that accepts every certificate. It must be
# compiled code: .NET runs the callback on a thread without a PowerShell
# runspace, so a script block fails there with "An unexpected error occurred
# on a send".
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

# Send one GET request with the enrollment token to the given URL. Returns an
# object with the HTTP StatusCode (0 when no response arrived), the
# ContentType, and the Error.
#
# This uses HttpWebRequest instead of Invoke-WebRequest. Windows PowerShell 5.1
# fails with "Operation is not valid due to the current state of the object"
# when Invoke-WebRequest receives a redirect it may not follow. The request
# object also takes the certificate callback per request, so no process-wide
# state changes.
function Invoke-EndpointProbe {
    param([string]$Uri, [bool]$SkipCertificateCheck)

    $request = [Net.HttpWebRequest]::Create($Uri)
    $request.Method = 'GET'
    $request.Timeout = 20000
    $request.ReadWriteTimeout = 20000
    # Redirects are reported as their own status: a wrong sub path often
    # redirects to the web interface, which would answer 200.
    $request.AllowAutoRedirect = $false
    # No pooled connections. A pooled connection that was opened without
    # verification would otherwise serve a later request with verification,
    # and skip the handshake.
    $request.KeepAlive = $false
    $request.Headers.Add('Authorization', "Bearer $Token")
    if ($SkipCertificateCheck) {
        $request.ServerCertificateValidationCallback = Get-TrustAllCertificatesCallback
    }

    $response = $null
    try {
        $response = $request.GetResponse()
    } catch [Net.WebException] {
        # An HTTP error status still proves that the connection works.
        if (-not $_.Exception.Response) {
            return [PSCustomObject]@{ StatusCode = 0; ContentType = ''; Error = $_.Exception.Message }
        }
        $response = $_.Exception.Response
    } catch {
        return [PSCustomObject]@{ StatusCode = 0; ContentType = ''; Error = $_.Exception.Message }
    }

    try {
        return [PSCustomObject]@{
            StatusCode  = [int]$response.StatusCode
            ContentType = "$($response.ContentType)"
            Error       = $null
        }
    } finally {
        $response.Close()
    }
}

# The server base URL is the endpoint without a trailing slash and without the
# default OpAMP path, like the supervisor derives it.
function Get-AuthCheckUrl {
    param([string]$EndpointUrl)

    $base = $EndpointUrl.TrimEnd('/')
    if ($base.EndsWith('/v1/opamp', [StringComparison]::OrdinalIgnoreCase)) {
        $base = $base.Substring(0, $base.Length - '/v1/opamp'.Length)
    }
    return "$base$AuthCheckPath"
}

# Ask the server to validate the enrollment token before anything is installed.
# The supervisor does the same at startup, so a bad token fails here instead of
# in a crash loop. The check also explains the two common URL mistakes: The
# supervisor rejects a server certificate that the system does not trust, such
# as a self-signed one. When a request fails with verification but succeeds
# without it, the certificate is the problem, and the user needs
# -SkipTlsVerify. When an http:// URL fails but the same host answers on
# https://, the URL has the wrong scheme.
function Test-Endpoint {
    $isHttps = $Endpoint.StartsWith('https://', [StringComparison]::OrdinalIgnoreCase)
    $isHttp = $Endpoint.StartsWith('http://', [StringComparison]::OrdinalIgnoreCase)
    if (-not ($isHttps -or $isHttp)) {
        # Not an HTTP URL. Leave it to the supervisor to reject it.
        return
    }

    Write-Step "Checking connection to $Endpoint"
    $checkUrl = Get-AuthCheckUrl -EndpointUrl $Endpoint

    if ($isHttps) {
        if ($SkipTlsVerify) {
            Write-Warning "TLS certificate verification is disabled for $Endpoint."
            $probe = Invoke-EndpointProbe -Uri $checkUrl -SkipCertificateCheck $true
            if ($probe.StatusCode -eq 0) {
                throw "Cannot connect to ${Endpoint}: $($probe.Error)"
            }
        } else {
            $probe = Invoke-EndpointProbe -Uri $checkUrl -SkipCertificateCheck $false
            if ($probe.StatusCode -eq 0) {
                $insecureProbe = Invoke-EndpointProbe -Uri $checkUrl -SkipCertificateCheck $true
                if ($insecureProbe.StatusCode -ne 0) {
                    throw @"
The TLS certificate of $Endpoint is not trusted by this system.
If the Graylog server uses a self-signed certificate, run this script again
with -SkipTlsVerify. The Collector then skips certificate verification for
this server.
"@
                }
                throw "Cannot connect to ${Endpoint}: $($probe.Error)"
            }
        }
    } else {
        $probe = Invoke-EndpointProbe -Uri $checkUrl -SkipCertificateCheck $false
        if ($probe.StatusCode -eq 0) {
            $httpsEndpoint = 'https://' + $Endpoint.Substring('http://'.Length)
            $httpsCheckUrl = 'https://' + $checkUrl.Substring('http://'.Length)
            $httpsProbe = Invoke-EndpointProbe -Uri $httpsCheckUrl -SkipCertificateCheck $true
            if ($httpsProbe.StatusCode -ne 0) {
                throw "Cannot connect to $Endpoint, but the server answers on $httpsEndpoint. Run this script again with that URL."
            }
            throw "Cannot connect to ${Endpoint}: $($probe.Error)`nAlso tried ${httpsEndpoint}: $($httpsProbe.Error)"
        }
    }

    switch ($probe.StatusCode) {
        200 {
            # The real endpoint answers with an empty body. Graylog serves its
            # web interface for unknown paths, with the same status code.
            if ($probe.ContentType -like 'text/html*') {
                throw "$Endpoint points to the Graylog web interface, not to its API. Check the path of the URL. Usually the Graylog server URL has no path."
            }
            return
        }
        { $_ -in 401, 403 } {
            throw "The Graylog server at $Endpoint rejected the enrollment token (HTTP $_). Check the token, or create a new one in Graylog."
        }
        default {
            throw "Unexpected response HTTP $_ from $checkUrl. Is $Endpoint the Graylog server URL?"
        }
    }
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

    $pattern = '^(?<digest>[0-9a-fA-F]{64})\s+(?<name>graylog-collector-\S+\.msi)$'
    $entry = $manifest -split "`r?`n" | Where-Object { $_ -match $pattern } | Select-Object -First 1
    if (-not $entry) {
        throw 'Release manifest has no Windows installer (.msi).'
    }
    $null = $entry -match $pattern

    # The asset sits next to the manifest. That also works for "latest".
    $baseUrl = $manifestUrl.Substring(0, $manifestUrl.LastIndexOf('/'))
    return [PSCustomObject]@{
        Name   = $Matches.name
        Digest = $Matches.digest.ToLower()
        Url    = "$baseUrl/$($Matches.name)"
    }
}

function Test-Checksum {
    param($Asset, [string]$FilePath)

    Write-Step 'Verifying SHA-256 checksum'
    $actual = (Get-FileHash -Path $FilePath -Algorithm SHA256).Hash.ToLower()
    if ($actual -ne $Asset.Digest) {
        throw "Checksum mismatch for $($Asset.Name) (expected $($Asset.Digest), got $actual)."
    }
}

# No installer log: Windows Installer writes the resolved registry values,
# including the token, into it. To debug a failing installation, download the
# MSI and run it by hand with msiexec /l*v.
function Install-Msi {
    param([string]$MsiPath, [string]$MsiUrl)

    $arguments = @(
        '/i', "`"$MsiPath`"",
        '/qn',
        '/norestart',
        "ENROLLENDPOINT=`"$Endpoint`"",
        "ENROLLTOKEN=`"$Token`""
    )
    if ($SkipTlsVerify) {
        $arguments += 'ENROLLINSECURETLS=true'
    }

    $process = Start-Process -FilePath 'msiexec.exe' -ArgumentList $arguments -Wait -PassThru
    switch ($process.ExitCode) {
        0 { return }
        3010 {
            Write-Warning 'Installation finished. A reboot is required to complete it.'
            return
        }
        default { throw "msiexec failed with exit code $($process.ExitCode). Download $MsiUrl and run it with msiexec /i <file> /l*v <log> to see details." }
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

    Write-Step "Downloading $($asset.Url)"
    Invoke-WebRequest -Uri $asset.Url -OutFile $msiPath -UseBasicParsing

    Test-Checksum -Asset $asset -FilePath $msiPath

    Write-Step "Installing $($asset.Name)"
    Install-Msi -MsiPath $msiPath -MsiUrl $asset.Url
} finally {
    Remove-Item -Path $tempDir -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Step "Graylog Collector installed ($($asset.Name))"
Write-Host "Check the service status with: Get-Service $ServiceName"
