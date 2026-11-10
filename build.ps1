$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

Write-Host '==> frontend'
Push-Location frontend
npm install
if ($LASTEXITCODE -ne 0) { throw 'npm install failed' }
npm run build
if ($LASTEXITCODE -ne 0) { throw 'frontend build failed' }
Pop-Location

Write-Host '==> backend tests'
Push-Location backend
go test ./...
if ($LASTEXITCODE -ne 0) { throw 'go test failed' }
Pop-Location

Write-Host '==> backend build'
Push-Location backend
go build -o ../apiunit.exe ./cmd/server
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
Pop-Location

Write-Host ''
Write-Host 'Build complete: .\apiunit.exe'
Write-Host 'Run:  .\apiunit.exe'
Write-Host '      (WEB_DIR is auto-detected as backend/web)'
