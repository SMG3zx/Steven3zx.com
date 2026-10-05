#!/usr/bin/env pwsh
# Janus Mission Environment Setup (Idempotent)
# This runs at the start of each worker session on Windows/PowerShell

$ErrorActionPreference = "Continue"

# Install backend Go dependencies
Write-Host "Installing Go dependencies..."
Push-Location E:\Janus\backend\janus-api
go mod download
Pop-Location

# Install frontend Node dependencies
Write-Host "Installing Node dependencies..."
Push-Location E:\Janus\frontend\web
npm install --prefer-offline 2>$null
Pop-Location

# Ensure .env.local exists with placeholders (do not overwrite if present)
if (-not (Test-Path E:\Janus\.env.local)) {
    Write-Host "Creating .env.local with placeholders..."
    @"
# Cloudflare DNS Integration
CF_API_TOKEN=
CF_ZONE_ID=

# Stripe Billing
STRIPE_SECRET_KEY=
STRIPE_PUBLISHABLE_KEY=
STRIPE_WEBHOOK_SECRET=

# Janus Core (defaults for local dev)
JANUS_DATABASE_URL=YOUR_DATABASE_URL_HERE
JANUS_JWT_SECRET=dev-secret-change-in-production
JANUS_ENV=dev
"@ | Out-File -FilePath E:\Janus\.env.local -Encoding utf8
}

Write-Host "Environment setup complete."
