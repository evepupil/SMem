$ErrorActionPreference = "Stop"

function Invoke-Checked {
    param([string]$Label, [scriptblock]$Cmd)
    Write-Host "==> $Label"
    & $Cmd
    if ($LASTEXITCODE -ne 0) {
        throw "Command failed with exit code $LASTEXITCODE`: $Label"
    }
}

Invoke-Checked "go vet ./..." { go vet ./... }
Invoke-Checked "go test -count=1 ./..." { go test -count=1 -v ./... }

New-Item -ItemType Directory -Force -Path ".\bin" | Out-Null
Invoke-Checked "go build -o .\bin\smem.exe ./cmd/smem" { go build -o ".\bin\smem.exe" ./cmd/smem }
Invoke-Checked "smoke test: bin/smem.exe version" { .\bin\smem.exe version }

Write-Host "==> All checks passed!"
