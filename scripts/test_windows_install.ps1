param(
    [Parameter(Mandatory)][string]$Assets,
    [Parameter(Mandatory)][string]$Version,
    [Parameter(Mandatory)][string]$Revision
)

$ErrorActionPreference = 'Stop'
$releaseRoot = (Resolve-Path -LiteralPath $Assets).Path
$ciAssets = Join-Path $releaseRoot $Version
$ciArchive = "datalens_${Version}_windows_amd64.zip"
$ciReleases = 'https://github.com/datalens-tech/datalens-cli/releases'
$ciManifestUri = "$ciReleases/latest/download/SHA256SUMS"
$ciArchiveUri = "$ciReleases/download/v$Version/$ciArchive"
$ciBreakChecksum = $false
$ciOtherArchives = foreach ($other in "$Version-beta.10", "v$Version", "0$Version", "$Version+local") {
    ('0' * 64) + "  datalens_${other}_windows_amd64.zip"
}

function gh { throw 'Unexpected fallback to GitHub CLI during the offline install test.' }

function Test-RpcExamples {
    $ErrorActionPreference = 'Continue'
    $helpText = (& $exe rpc getWorkbook --profile=smoke --help) -join "`n"
    if ($LASTEXITCODE -ne 0) { throw 'Could not get RPC help.' }
    $errorText = (& $exe rpc getDashboard --profile=smoke --auth-token= --format=json 2>&1) -join "`n"
    $problem = ($errorText | ConvertFrom-Json -ErrorAction Stop).error
    if ($LASTEXITCODE -ne 2 -or $problem.code -ne 'INVALID_USAGE') { throw 'Expected a missing dashboard ID.' }

    foreach ($text in $helpText, $problem.message) {
        if ($text -notmatch '(?m)^  PowerShell:\r?\n    (datalens rpc (?:getWorkbook|getDashboard) [^\r\n]+ --profile=smoke)\r?$') {
            throw 'Missing RPC example with the selected profile.'
        }
        $example = [scriptblock]::Create($Matches[1].Replace('your-workbook-id', 'workbook with spaces') + ' --auth-token= --format=json')
        $result = (& $example 2>&1) -join "`n"
        if ($LASTEXITCODE -ne 3 -or ($result | ConvertFrom-Json -ErrorAction Stop).error.code -ne 'AUTH_TOKEN_INVALID') {
            throw "The generated PowerShell example failed argument validation: $result"
        }
        if ($text -notmatch '(?m)^  Git Bash:\r?\n    (datalens rpc (?:getWorkbook|getDashboard) [^\r\n]+ --profile=smoke)\r?$') {
            throw 'Missing Git Bash example with the selected profile.'
        }
        $script = Join-Path $sandbox 'rpc-example.sh'
        [IO.File]::WriteAllText($script, $Matches[1] + ' --auth-token= --format=json')
        $bash = Join-Path $env:ProgramFiles 'Git\bin\bash.exe'
        $result = (& $bash $script 2>&1) -join "`n"
        if ($LASTEXITCODE -ne 3 -or ($result | ConvertFrom-Json -ErrorAction Stop).error.code -ne 'AUTH_TOKEN_INVALID') {
            throw "The generated Git Bash example failed argument validation: $result"
        }
    }
}

function Invoke-WebRequest {
    [CmdletBinding()]
    param([uri]$Uri, [string]$OutFile, [switch]$UseBasicParsing)

    switch ($Uri.AbsoluteUri) {
        $ciManifestUri {
            $manifest = [IO.File]::ReadAllText((Join-Path $ciAssets 'SHA256SUMS'))
            $entries = @($ciOtherArchives) + $manifest
            [IO.File]::WriteAllText($OutFile, ($entries -join "`n"))
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
foreach ($name in 'LOCALAPPDATA', 'APPDATA', 'DATALENS_INSTALL_DIR', 'DATALENS_CONFIG_DIR', 'DATALENS_CLI_INSTALL_VERSION', 'PATH') {
    $savedEnv[$name] = [Environment]::GetEnvironmentVariable($name)
}
try {
    $env:LOCALAPPDATA = Join-Path $sandbox 'Local'
    $env:APPDATA = Join-Path $sandbox 'Roaming'
    $env:DATALENS_INSTALL_DIR = $null
    $env:DATALENS_CONFIG_DIR = $null
    $env:DATALENS_CLI_INSTALL_VERSION = $null
    $messages = & $install

    $exe = Join-Path $env:LOCALAPPDATA 'Programs\DataLens\bin\datalens.exe'
    $quotedExe = $exe.Replace("'", "''")
    if ($messages[-1].Trim() -ne "& '$quotedExe' setup") { throw 'Incorrect setup command for the installed path.' }
    $info = & $exe version --format=json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $info.version -ne $Version -or $info.revision -ne $Revision -or
        $info.os -ne 'windows' -or $info.arch -ne 'amd64') {
        throw 'Unexpected build information from the installed release.'
    }

    $created = & $exe profiles create smoke --installation-type=on-premises --endpoint=https://127.0.0.1:1 --format=json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $created.schemaWarning -notlike '*schema update --profile=smoke*') {
        throw 'Expected an offline profile with schema recovery instructions.'
    }
    $config = Join-Path $env:APPDATA 'datalens\config.json'
    $profiles = (Get-Content -LiteralPath $config -Raw | ConvertFrom-Json).profiles
    if ($profiles.smoke.installationType -ne 'on-premises') { throw 'Could not find the saved on-premises profile.' }

    go test -count=1 -run '^TestRefreshInstalledProfileSchema$' ./internal/cli -args -windows-install-config (Split-Path -Parent $config)
    if ($LASTEXITCODE -ne 0) { throw 'Could not prepare the offline schema for the installed CLI.' }
    $schema = & $exe schema show getWorkbook --profile=smoke | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $schema.required -notcontains 'workbookId') { throw 'Installed CLI could not load the cached workbook schema.' }
    $configHash = (Get-FileHash -LiteralPath $config).Hash
    $env:PATH = (Split-Path -Parent $exe) + ';' + $env:PATH
    $env:DATALENS_CLI_INSTALL_VERSION = $Version
    $ciManifestUri = "$ciReleases/download/v$Version/SHA256SUMS"
    $messages = & $install
    if ($messages[-1].Trim() -ne "& '$quotedExe' setup") { throw 'Incorrect setup command for a binary on PATH.' }
    if ((Get-FileHash -LiteralPath $config).Hash -ne $configHash) { throw 'Reinstall changed the profile.' }
    if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) { Test-RpcExamples }

    $beta = "$Version-beta.2"
    $env:DATALENS_CLI_INSTALL_VERSION = "v$beta"
    $ciManifestUri = "$ciReleases/download/v$beta/SHA256SUMS"
    try {
        & $install
        throw 'Installer accepted a different version from a pinned manifest.'
    } catch {
        if ($_.Exception.Message -notlike 'Missing Windows/* archive in SHA256SUMS.') { throw }
    }

    $ciAssets = Join-Path $releaseRoot $beta
    $ciArchive = "datalens_${beta}_windows_amd64.zip"
    $ciArchiveUri = "$ciReleases/download/v$beta/$ciArchive"
    $oldBin = Join-Path $sandbox 'old-bin'
    $null = [IO.Directory]::CreateDirectory($oldBin)
    Copy-Item -LiteralPath $exe -Destination $oldBin
    $env:PATH = $oldBin + ';' + $env:PATH
    $messages = & $install
    if ($messages[0] -ne "Installed DataLens CLI v$beta to $exe") { throw 'Unexpected pinned beta installation result.' }
    if ($messages[-1].Trim() -ne "& '$quotedExe' setup") { throw 'Setup would run another executable from PATH.' }

    $info = & $exe version --format=json | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or $info.version -ne $beta -or $info.revision -ne $Revision) {
        throw 'The installed binary is not the pinned beta build.'
    }
    $exeHash = (Get-FileHash -LiteralPath $exe).Hash

    $ciBreakChecksum = $true
    try {
        & $install
        throw 'Installer accepted a corrupt ZIP.'
    } catch {
        if ($_.Exception.Message -notlike '*SHA-256*') { throw }
    }
    foreach ($invalid in 'latest', 'vv1.2.3', '01.2.3', '1.2.3-beta.01', '1.2.3/path') {
        $env:DATALENS_CLI_INSTALL_VERSION = $invalid
        try {
            & $install
            throw "Installer accepted invalid version $invalid."
        } catch {
            if ($_.Exception.Message -notlike 'DATALENS_CLI_INSTALL_VERSION must be*') { throw }
        }
    }
    if ((Get-FileHash -LiteralPath $exe).Hash -ne $exeHash -or
        (Get-FileHash -LiteralPath $config).Hash -ne $configHash) {
        throw 'Failed installation changed the executable or profile.'
    }
} finally {
    foreach ($name in $savedEnv.Keys) { [Environment]::SetEnvironmentVariable($name, $savedEnv[$name]) }
    if (Test-Path -LiteralPath $sandbox) { Remove-Item -LiteralPath $sandbox -Recurse -Force }
}

exit 0
