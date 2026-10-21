$ErrorActionPreference = 'Stop'
$rootlensBinary = Join-Path $PSScriptRoot 'dist\rootlens-windows-amd64.exe'
if (-not (Test-Path -LiteralPath $rootlensBinary)) {
    throw 'Build the Windows hub as described in README.md, or use the packaged preview archive.'
}
Write-Host 'Open http://127.0.0.1:8765. Press Ctrl+C to stop the synthetic, read-only preview.'
& $rootlensBinary demo --data (Join-Path $PSScriptRoot 'data\demo') --listen '127.0.0.1:8765'
