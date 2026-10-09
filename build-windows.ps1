$ErrorActionPreference = "Stop"

# Run from an MSYS2 UCRT64 terminal/PowerShell with GTK3, GCC, pkg-config,
# and Go installed. Ensure the GTK runtime bin directory is on PATH.
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw "Go is not on PATH." }
if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) { throw "GCC is not on PATH; use MSYS2 UCRT64." }
if (-not (Get-Command pkg-config -ErrorAction SilentlyContinue)) { throw "pkg-config is not on PATH." }

$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -buildid=" -o baretube-backend.exe main.go scrape.go
if ($LASTEXITCODE -ne 0) { throw "Go backend build failed." }

$cflags = ((pkg-config --cflags gtk+-3.0) -join " ")
$libs = ((pkg-config --libs gtk+-3.0) -join " ")
$command = "gcc -std=c11 -O2 -s -Wall -Wextra $cflags -o baretube-bin.exe main.c home.c config.c $libs"
cmd.exe /c $command
if ($LASTEXITCODE -ne 0) { throw "GTK frontend build failed." }

Write-Host "Built baretube-bin.exe and baretube-backend.exe. Keep the GTK runtime DLLs and mpv.exe available on PATH."
