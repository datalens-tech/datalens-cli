param(
    [Parameter(Mandatory)][string]$Assets,
    [Parameter(Mandatory)][string]$Version,
    [Parameter(Mandatory)][string]$Revision
)

$ErrorActionPreference = 'Stop'
$ciAssets = (Resolve-Path -LiteralPath $Assets).Path
$ciArchive = "datalens_${Version}_windows_amd64.zip"
$ciReleases = 'https://github.com/datalens-tech/datalens-cli/releases'
$ciArchiveUri = "$ciReleases/download/v$Version/$ciArchive"
$ciBreakChecksum = $false

function gh { throw 'Unexpected fallback to GitHub CLI during the offline install test.' }

function Invoke-WebRequest {
    [CmdletBinding()]
    param([uri]$Uri, [string]$OutFile, [switch]$UseBasicParsing)

    switch ($Uri.AbsoluteUri) {
        "$ciReleases/latest/download/SHA256SUMS" {
            Copy-Item -LiteralPath (Join-Path $ciAssets 'SHA256SUMS') -Destination $OutFile
        }
        $ciArchiveUri {
            if ($ciBreakChecksum) {
                [IO.File]::WriteAllBytes($OutFile, [byte[]](0))
            } else {
                Copy-Item -LiteralPath (Join-Path $ciAssets $ciArchive) -Destination $OutFile
            }
        }
        default { throw "Unexpected release URL: $Uri" }
    }
}

$install = [scriptblock]::Create([IO.File]::ReadAllText((Join-Path $ciAssets 'install.ps1')))
$sandbox = Join-Path ([IO.Path]::GetTempPath()) ("User's DataLens test " + [guid]::NewGuid())
$savedEnv = @{}
foreach ($name in 'LOCALAPPDATA', 'APPDATA', 'DATALENS_INSTALL_DIR', 'DATALENS_CONFIG_DIR', 'PATH') {
    $savedEnv[$name] = [Environment]::GetEnvironmentVariable($name)
}
try {
    $env:LOCALAPPDATA = Join-Path $sandbox 'Local'
    $env:APPDATA = Join-Path $sandbox 'Roaming'
    $env:DATALENS_INSTALL_DIR = $null
    $env:DATALENS_CONFIG_DIR = $null
    $messages = & $install

    $exe = Join-Path $env:LOCALAPPDATA 'Programs\DataLens\bin\datalens.exe'
    $quotedExe = $exe.Replace("'", "''")
    if ($messages[-1].Trim() -ne "& '$quotedExe' setup") { throw 'Incorrect setup command for the installed path.' }
    $info = & $exe version --format=json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $info.version -ne $Version -or $info.revision -ne $Revision -or
        $info.os -ne 'windows' -or $info.arch -ne 'amd64') {
        throw 'Unexpected build information from the installed release.'
    }

    & $exe profiles create smoke --installation-type=cloud
    if ($LASTEXITCODE -ne 0) { throw 'Could not create the smoke profile.' }
    $config = Join-Path $env:APPDATA 'datalens\config.json'
    $profiles = (Get-Content -LiteralPath $config -Raw | ConvertFrom-Json).profiles
    if ($profiles.smoke.installationType -ne 'cloud') { throw 'Could not find the saved Cloud profile.' }

    $configHash = (Get-FileHash -LiteralPath $config).Hash
    $env:PATH = (Split-Path -Parent $exe) + ';' + $env:PATH
    $messages = & $install
    if ($messages[-1].Trim() -ne 'datalens setup') { throw 'Incorrect setup command for a binary on PATH.' }
    if ((Get-FileHash -LiteralPath $config).Hash -ne $configHash) { throw 'Reinstall changed the profile.' }
    $exeHash = (Get-FileHash -LiteralPath $exe).Hash

    $ciBreakChecksum = $true
    try {
        & $install
        throw 'Installer accepted a corrupt ZIP.'
    } catch {
        if ($_.Exception.Message -notlike '*SHA-256*') { throw }
    }
    if ((Get-FileHash -LiteralPath $exe).Hash -ne $exeHash -or
        (Get-FileHash -LiteralPath $config).Hash -ne $configHash) {
        throw 'Failed installation changed the executable or profile.'
    }
} finally {
    foreach ($name in $savedEnv.Keys) { [Environment]::SetEnvironmentVariable($name, $savedEnv[$name]) }
    if (Test-Path -LiteralPath $sandbox) { Remove-Item -LiteralPath $sandbox -Recurse -Force }
}
