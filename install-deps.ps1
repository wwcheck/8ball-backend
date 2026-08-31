# Sud8Ball Backend - Install Dependencies
# Simplified version without special characters

Write-Host "Installing Sud8Ball Backend Dependencies..." -ForegroundColor Green
Write-Host ""

# Check if go.mod exists
if (-not (Test-Path "go.mod")) {
    Write-Host "ERROR: go.mod file not found!" -ForegroundColor Red
    exit 1
}

Write-Host "OK: go.mod file found" -ForegroundColor Green
Write-Host ""

# Define packages to install
$packages = @(
    "github.com/gin-gonic/gin@v1.9.1",
    "github.com/gorilla/websocket@v1.5.0",
    "google.golang.org/grpc@latest",
    "google.golang.org/protobuf@latest",
    "github.com/golang-jwt/jwt/v5@latest",
    "github.com/go-sql-driver/mysql@latest",
    "github.com/redis/go-redis/v9@latest",
    "github.com/stretchr/testify@latest",
    "github.com/sirupsen/logrus@latest",
    "github.com/prometheus/client_golang@latest"
)

$total = $packages.Count
$current = 1

Write-Host "Starting installation of $total packages..." -ForegroundColor Cyan
Write-Host ""

foreach ($pkg in $packages) {
    Write-Host "[$current/$total] Installing: $pkg" -ForegroundColor Cyan

    try {
        & go get $pkg
        Write-Host "  OK" -ForegroundColor Green
    } catch {
        Write-Host "  FAILED: $_" -ForegroundColor Red
    }

    Write-Host ""
    $current++
}

# Tidy dependencies
Write-Host "Running: go mod tidy" -ForegroundColor Cyan
& go mod tidy

Write-Host ""
Write-Host "Running: go mod verify" -ForegroundColor Cyan
& go mod verify

Write-Host ""
Write-Host "Installation Complete!" -ForegroundColor Green
