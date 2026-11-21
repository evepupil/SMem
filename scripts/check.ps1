param([switch]$RealCodex)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$oldCheck = $env:SMEM_REAL_CODEX
Push-Location -LiteralPath $root
try {
    $goFiles = @(Get-ChildItem -Path cmd, internal -Filter '*.go' -Recurse -File | ForEach-Object FullName)
    $unformatted = @(& gofmt -l @goFiles)
    if ($LASTEXITCODE -ne 0 -or $unformatted.Count) { throw "gofmt: $unformatted" }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    $env:SMEM_REAL_CODEX = if ($RealCodex) { '1' } else { '0' }
    & go test -race -count=1 ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test -race failed (requires CGO and a C compiler)' }
    $markdown = @(Get-Item README.md) + @(Get-ChildItem -Path docs -Recurse -File -Filter '*.md')
    foreach ($file in $markdown) {
        $content = Get-Content -Raw -Encoding utf8 -LiteralPath $file.FullName
        foreach ($link in [regex]::Matches($content, '\[[^\]]+\]\((?!https?://)([^)]+)\)')) {
            $parts = $link.Groups[1].Value -split '#', 2
            $target = if ($parts[0]) { Join-Path $file.DirectoryName $parts[0] } else { $file.FullName }
            if (-not (Test-Path -LiteralPath $target -PathType Leaf)) { throw "Broken link: $($file.Name) -> $target" }
            if ($parts.Count -eq 2) {
                $source = Get-Content -Raw -Encoding utf8 -LiteralPath $target
                if (-not $source.Contains('id="' + $parts[1] + '"')) { throw "Missing anchor: $($link.Value)" }
            }
        }
    }
    & go build -trimpath -o bin/mem.exe ./cmd/mem
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
    & ./bin/mem.exe --help
    if ($LASTEXITCODE -ne 0) { throw 'compiled command failed' }
    Write-Output 'PASS: format, vet, race tests, document links, build and executable'
} finally {
    $env:SMEM_REAL_CODEX = $oldCheck
    Pop-Location
}
