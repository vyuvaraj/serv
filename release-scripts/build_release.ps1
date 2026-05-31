# build_release.ps1 - Automated Release Packager for Windows & Linux
# Run this from the root of the project directory.

$ErrorActionPreference = "Stop"

# Define directories
$srcDir = Get-Location
$distDir = Join-Path $srcDir "dist"
$winPkgDir = Join-Path $distDir "serv-windows-amd64"
$linuxPkgDir = Join-Path $distDir "serv-linux-amd64"

# 1. Clean and create dist directories
Write-Host "Cleaning old build files..." -ForegroundColor Cyan
if (Test-Path $distDir) {
    Remove-Item -Recurse -Force $distDir
}
New-Item -ItemType Directory -Force -Path $distDir | Out-Null
New-Item -ItemType Directory -Force -Path $winPkgDir | Out-Null
New-Item -ItemType Directory -Force -Path $linuxPkgDir | Out-Null

# 2. Build binaries
Write-Host "Building Windows binary (serv.exe)..." -ForegroundColor Green
& go build -o (Join-Path $winPkgDir "serv.exe") main.go

Write-Host "Cross-compiling Linux binary (serv)..." -ForegroundColor Green
$env:GOOS = "linux"
$env:GOARCH = "amd64"
& go build -o (Join-Path $linuxPkgDir "serv") main.go
# Reset Go environmental variables
$env:GOOS = ""
$env:GOARCH = ""

# 3. Define common files/folders to copy
$commonPaths = @(
    "compiler",
    "runtime",
    "scripts",
    "examples",
    "vscode-support",
    "go.mod",
    "go.sum",
    "README.md",
    "main.srv",
    "test_sample.srv"
)

# 4. Copy common files/folders to both target packages
Write-Host "Structuring package files..." -ForegroundColor Cyan
foreach ($path in $commonPaths) {
    $fullSrcPath = Join-Path $srcDir $path
    if (Test-Path $fullSrcPath) {
        Copy-Item -Path $fullSrcPath -Destination $winPkgDir -Recurse -Force
        Copy-Item -Path $fullSrcPath -Destination $linuxPkgDir -Recurse -Force
    }
}

# 5. Compress packages
Write-Host "Creating zip package for Windows (serv-windows-amd64.zip)..." -ForegroundColor Yellow
$winZipPath = Join-Path $distDir "serv-windows-amd64.zip"
Compress-Archive -Path "$winPkgDir\*" -DestinationPath $winZipPath -Force

Write-Host "Creating tar.gz package for Linux (serv-linux-amd64.tar.gz)..." -ForegroundColor Yellow
# Run native tar command available in Windows 10/11
$tarGzPath = Join-Path $distDir "serv-linux-amd64.tar.gz"
$oldLocation = Get-Location
Set-Location $distDir
& tar -czf "serv-linux-amd64.tar.gz" "serv-linux-amd64"
Set-Location $oldLocation

# 6. Clean intermediate directories
Write-Host "Cleaning up intermediate package files..." -ForegroundColor Cyan
Remove-Item -Recurse -Force $winPkgDir
Remove-Item -Recurse -Force $linuxPkgDir

Write-Host "Done! Packages successfully generated in the dist/ folder:" -ForegroundColor Green
Write-Host "  - Windows: dist/serv-windows-amd64.zip" -ForegroundColor White
Write-Host "  - Linux:   dist/serv-linux-amd64.tar.gz" -ForegroundColor White
