# Installs gg, the Gagarin CLI, on Windows.
#
#   irm https://gagarin.cloud/install.ps1 | iex
#
# It downloads the release archive for this machine, checks it against the
# release's checksums.txt, and refuses to install anything that does not match.
# No administrator rights: gg goes to %LOCALAPPDATA%\Programs\gg, and that
# directory is added to the user PATH (a GAGARIN_INSTALL_DIR is left off it).
#
# Environment:
#   GAGARIN_INSTALL_DIR      where to put gg.exe (default %LOCALAPPDATA%\Programs\gg)
#   GAGARIN_INSTALL_VERSION  a release to install, e.g. v0.54.0 (default: latest)
#   GAGARIN_RELEASES         where releases live (default the GitHub releases page)
#
# The script block keeps its variables out of the caller's session, and it
# throws rather than calling exit: under `iex`, exit would close the window.

& {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    # Windows PowerShell 5.1 still offers TLS 1.0 first; GitHub refuses it.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    Add-Type -AssemblyName System.Net.Http

    $releases = if ($env:GAGARIN_RELEASES) { $env:GAGARIN_RELEASES.TrimEnd('/') } else { 'https://github.com/gagarin-cloud/gg/releases' }

    # A 32-bit PowerShell on 64-bit Windows reports x86 here and the real
    # architecture in PROCESSOR_ARCHITEW6432.
    $cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    $arch = switch ($cpu) {
        'AMD64' { 'amd64' }
        'ARM64' { 'arm64' }
        default { throw "gg install: no gg build for $cpu; go install github.com/gagarin-cloud/gg@latest builds one" }
    }

    if ($env:GAGARIN_INSTALL_VERSION) {
        $tag = 'v' + $env:GAGARIN_INSTALL_VERSION.TrimStart('v')
    } else {
        # /releases/latest redirects to /releases/tag/<tag>. Reading the
        # redirect rather than the API sidesteps its anonymous rate limit.
        $handler = New-Object System.Net.Http.HttpClientHandler
        $handler.AllowAutoRedirect = $false
        $client = New-Object System.Net.Http.HttpClient($handler)
        try {
            $resp = $client.GetAsync("$releases/latest").GetAwaiter().GetResult()
            $location = $resp.Headers.Location
        } finally {
            $client.Dispose()
        }
        if (-not $location) { throw "gg install: cannot reach $releases" }
        $tag = ($location.ToString() -split '/')[-1]
    }
    if ($tag -notmatch '^v[0-9][0-9A-Za-z.-]*$') { throw "gg install: `"$tag`" is not a release version" }
    $ver = $tag.Substring(1)

    $asset = "gg_${ver}_windows_${arch}.zip"
    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("gg-install-" + [Guid]::NewGuid())
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Host "downloading gg $tag for windows/$arch"
        Invoke-WebRequest -UseBasicParsing -Uri "$releases/download/$tag/$asset" -OutFile (Join-Path $tmp $asset)
        Invoke-WebRequest -UseBasicParsing -Uri "$releases/download/$tag/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

        $expected = $null
        foreach ($line in Get-Content (Join-Path $tmp 'checksums.txt')) {
            $fields = $line -split '\s+'
            if ($fields.Count -ge 2 -and $fields[1] -eq $asset) { $expected = $fields[0].ToLowerInvariant() }
        }
        if (-not $expected) { throw "gg install: checksums.txt for $tag has no entry for $asset; refusing to install" }
        # .NET rather than Get-FileHash and Expand-Archive: those live in modules
        # that Windows PowerShell fails to load when started from PowerShell 7.
        $stream = [IO.File]::OpenRead((Join-Path $tmp $asset))
        try {
            $sha = [Security.Cryptography.SHA256]::Create()
            $actual = ([BitConverter]::ToString($sha.ComputeHash($stream)) -replace '-', '').ToLowerInvariant()
        } finally {
            $stream.Dispose()
        }
        if ($expected -ne $actual) {
            throw "gg install: $asset does not match its checksum (expected $expected, got $actual); refusing to install"
        }

        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [IO.Compression.ZipFile]::ExtractToDirectory((Join-Path $tmp $asset), (Join-Path $tmp 'x'))
        $new = Join-Path $tmp 'x\gg.exe'
        if (-not (Test-Path $new)) { throw "gg install: $asset has no gg.exe in it" }

        $dir = if ($env:GAGARIN_INSTALL_DIR) { $env:GAGARIN_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\gg' }
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        $exe = Join-Path $dir 'gg.exe'
        # Windows will not overwrite a running executable, but it will rename
        # one. gg deletes the .old it leaves behind the next time it starts.
        if (Test-Path $exe) {
            Remove-Item -Force -ErrorAction SilentlyContinue "$exe.old"
            Move-Item -Force $exe "$exe.old"
        }
        Copy-Item $new $exe

        Write-Host "installed $(& $exe version) to $exe"

        # The default directory is ours, so putting it on PATH is part of the
        # install. A directory somebody chose is theirs: say, do not touch.
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        $parts = if ($userPath) { $userPath -split ';' } else { @() }
        if ($parts -notcontains $dir) {
            if ($env:GAGARIN_INSTALL_DIR) {
                Write-Host "$dir is not on your PATH; add it to run gg by name"
            } else {
                [Environment]::SetEnvironmentVariable('Path', (($parts + $dir) -join ';').Trim(';'), 'User')
                Write-Host "added $dir to your user PATH; terminals opened from now on will find gg"
            }
        }
        if (($env:Path -split ';') -notcontains $dir) { $env:Path = "$dir;$env:Path" }

        $found = (Get-Command gg -ErrorAction SilentlyContinue | Select-Object -First 1).Source
        if ($found -and ($found -ne $exe)) {
            Write-Host ""
            Write-Host "warning: $found comes before $dir on PATH, so ``gg`` still runs that one."
        }

        Write-Host ""
        Write-Host "next: gg login"
    } finally {
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
    }
}
