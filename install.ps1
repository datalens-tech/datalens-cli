& {
    $ErrorActionPreference = 'Stop'
    $repo = 'datalens-tech/datalens-cli'
    $requestedVersion = $env:DATALENS_CLI_INSTALL_VERSION
    $tag = ''
    if ($requestedVersion) {
        $number = '(?:0|[1-9][0-9]*)'
        if ($requestedVersion -cnotmatch "^v?(?<version>$number\.$number\.$number(?:-beta\.$number)?)\z") {
            throw 'DATALENS_CLI_INSTALL_VERSION must be a release version such as 0.1.0 or v0.1.0-beta.1.'
        }
        $requestedVersion = $Matches.version
        $tag = "v$requestedVersion"
    }

    function Get-ReleaseAsset($Name, $Tag, $Destination) {
        $route = if ($Tag) { "download/$Tag" } else { 'latest/download' }
        try {
            Invoke-WebRequest -UseBasicParsing -Uri "https://github.com/$repo/releases/$route/$Name" -OutFile $Destination
        } catch {
            if (!(Get-Command gh -CommandType Application -ErrorAction SilentlyContinue)) { throw }
            [IO.File]::Delete($Destination)
            $tags = @()
            if ($Tag) { $tags += $Tag }
            gh release download @tags --repo "github.com/$repo" --pattern $Name --output $Destination
            if ($LASTEXITCODE -ne 0) { throw "Could not download $Name from GitHub." }
        }
    }

    $machine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    $arch = switch ($machine) {
        'AMD64' { 'amd64' }
        'x86'   { '386' }
        default { throw "No DataLens CLI release for Windows/$machine." }
    }

    $installDir = $env:DATALENS_INSTALL_DIR
    if (!$installDir) { $installDir = Join-Path $env:LOCALAPPDATA 'Programs\DataLens\bin' }
    if ($installDir -notmatch '^(?:[a-z]:[\\/]|\\\\[^\\]+\\[^\\]+)') { throw 'The installation directory must be an absolute path.' }
    $installDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($installDir)
    $configDir = $env:DATALENS_CONFIG_DIR
    if (!$configDir) { $configDir = Join-Path $env:APPDATA 'datalens' }
    $configDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($configDir)
    $target = Join-Path $installDir 'datalens.exe'
    $temp = Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
    $stage = Join-Path $installDir ('.datalens-install-' + [guid]::NewGuid())
    $null = [IO.Directory]::CreateDirectory($temp)
    try {
        $manifest = Join-Path $temp 'SHA256SUMS'
        Get-ReleaseAsset 'SHA256SUMS' $tag $manifest
        $versionPattern = if ($tag) { [regex]::Escape($requestedVersion) } else { 'v?[0-9]+\.[0-9]+\.[0-9]+(?:\+[0-9A-Za-z.-]+)?' }
        $archivePattern = "^datalens_(?<version>${versionPattern})_windows_${arch}\.zip$"
        $archive = $null
        foreach ($line in [IO.File]::ReadLines($manifest)) {
            $fields = $line.Trim() -split '\s+'
            if ($fields.Count -lt 2 -or $fields[1] -cnotmatch $archivePattern) { continue }
            $version = $Matches.version.TrimStart('v')
            if ($archive -or $fields.Count -ne 2 -or $fields[0] -notmatch '^[0-9a-fA-F]{64}$') {
                throw "Expected one valid Windows/$arch archive in SHA256SUMS."
            }
            $hash = $fields[0]
            $archive = $fields[1]
        }
        if (!$archive) { throw "Missing Windows/$arch archive in SHA256SUMS." }

        $zip = Join-Path $temp $archive
        Get-ReleaseAsset $archive "v$version" $zip
        if ((Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash -ne $hash) {
            throw "SHA-256 mismatch for $archive."
        }

        $null = [IO.Directory]::CreateDirectory($installDir)
        $existing = Get-Item -LiteralPath $target -Force -ErrorAction SilentlyContinue
        if ($existing -and ($existing.PSIsContainer -or ($existing.Attributes -band [IO.FileAttributes]::ReparsePoint))) {
            throw "Expected a regular file at $target."
        }
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $bundle = [IO.Compression.ZipFile]::OpenRead($zip)
        try {
            $entry = $bundle.GetEntry('datalens.exe')
            if (!$entry -or !$entry.Length) { throw "Missing or empty datalens.exe in $archive." }
            [IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $stage)
        } finally {
            $bundle.Dispose()
        }

        $config = [IO.Directory]::CreateDirectory($configDir)
        if ($config.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "The configuration directory is a link: $configDir" }
        if ($existing) {
            [IO.File]::Replace($stage, $target, [NullString]::Value)
        } else {
            [IO.File]::Move($stage, $target)
        }
        Write-Output "Installed DataLens CLI v$version to $target"
        $setupCommand = 'datalens'
        if ($installDir -notin ($env:PATH -split ';')) {
            Write-Output "Add this directory to your PATH: $installDir"
            $escapedTarget = [Management.Automation.Language.CodeGeneration]::EscapeSingleQuotedStringContent($target)
            $setupCommand = "& '$escapedTarget'"
        }
        Write-Output "To configure a profile, run:"
        Write-Output "  $setupCommand setup"
    } finally {
        if ([IO.File]::Exists($stage)) { [IO.File]::Delete($stage) }
        [IO.Directory]::Delete($temp, $true)
    }
}
