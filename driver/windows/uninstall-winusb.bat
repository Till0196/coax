@echo off
rem Copyright 2026 Till0196
rem SPDX-License-Identifier: Apache-2.0
rem
rem Undoes install-winusb.bat: removes the coax driver packages from the driver
rem store and deletes the self-signed certificate it created. Double-click it,
rem or drag specific *-winusb.inf onto it to remove only those.
rem
rem Same shape as install-winusb.bat: the batch half reads the file back and
rem hands the PowerShell half to PowerShell.

setlocal EnableExtensions
set "COAX_WINUSB_SELF=%~f0"
set "COAX_WINUSB_INF="
for %%F in (%*) do call :append "%%~nxF"
powershell -NoProfile -ExecutionPolicy Bypass -Command "$m='#@P'+'S'; $s=Get-Content -LiteralPath $env:COAX_WINUSB_SELF -Raw; Invoke-Expression (($s -split $m,2)[1])"
exit /b %ERRORLEVEL%

:append
set "COAX_WINUSB_INF=%COAX_WINUSB_INF%%~1|"
goto :eof

#@PS
$ErrorActionPreference = 'Stop'

function Quote([string]$s) { "'" + ($s -replace "'", "''") + "'" }

$self = $env:COAX_WINUSB_SELF
$wanted = @()
if ($env:COAX_WINUSB_INF) {
  $wanted = @($env:COAX_WINUSB_INF -split '\|' | Where-Object { $_ })
}
# Only coax's own packages: the INFs shipped beside this script, unless some
# were named. Another vendor's package may follow the same naming.
$ours = $wanted
if ($ours.Count -eq 0) {
  $ours = @(Get-ChildItem -LiteralPath (Split-Path -Parent $self) -Filter '*-winusb.inf' | ForEach-Object { $_.Name })
}

$id = [Security.Principal.WindowsIdentity]::GetCurrent()
$isAdmin = ([Security.Principal.WindowsPrincipal]$id).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
  $boot = '$m=' + (Quote ('#@P' + 'S')) + '; $s=Get-Content -LiteralPath $env:COAX_WINUSB_SELF -Raw; Invoke-Expression (($s -split $m,2)[1])'
  $cmd = '$env:COAX_WINUSB_SELF=' + (Quote $self) + '; $env:COAX_WINUSB_INF=' + (Quote ($wanted -join '|')) + '; ' + $boot
  Start-Process -Verb RunAs -FilePath 'powershell.exe' `
    -ArgumentList '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', $cmd | Out-Null
  exit 0
}

# pnputil prints one blank-line-separated record per package, with localized
# labels. Match on the values instead: the record holds its published oemN.inf
# name, the original *-winusb.inf name, and the provider, which coax's INFs
# all give as "coax".
function Ours {
  $found = @()
  $records = ((& pnputil.exe /enum-drivers) -join "`n") -split "`n[ \t]*`n"
  foreach ($rec in $records) {
    $oem = [regex]::Match($rec, '(?im)\b(oem\d+\.inf)\b')
    $orig = [regex]::Match($rec, '(?im)\b([A-Za-z0-9_.-]+-winusb\.inf)\b')
    if (-not $oem.Success -or -not $orig.Success) { continue }
    if ($rec -notmatch '(?im)[:\uFF1A]\s*coax\s*$') { continue }
    if ($ours -notcontains $orig.Groups[1].Value) { continue }
    $found += [pscustomobject]@{ Oem = $oem.Groups[1].Value; Name = $orig.Groups[1].Value }
  }
  return $found
}

$removed = 0
$failed = 0
foreach ($package in @(Ours)) {
  $name = $package.Name

  Write-Host ''
  Write-Host "== $name ($($package.Oem))" -ForegroundColor Cyan
  & pnputil.exe /delete-driver $package.Oem /uninstall
  if ($LASTEXITCODE -eq 0 -or $LASTEXITCODE -eq 3010) {
    $removed++
  } else {
    Write-Host "pnputil failed with exit code $LASTEXITCODE." -ForegroundColor Red
    $failed++
  }
}

if ($removed -eq 0 -and $failed -eq 0) {
  Write-Host 'No coax WinUSB driver package is installed.'
}

# The certificate is only worth keeping while a package signed by it remains.
$ours = @(Get-ChildItem -LiteralPath (Split-Path -Parent $self) -Filter '*-winusb.inf' | ForEach-Object { $_.Name })
if (@(Ours).Count -eq 0) {
  $subject = 'CN=coax WinUSB signing'
  foreach ($store in 'My', 'Root', 'TrustedPublisher') {
    Get-ChildItem "Cert:\LocalMachine\$store" |
      Where-Object { $_.Subject -eq $subject } |
      ForEach-Object {
        Remove-Item -LiteralPath $_.PSPath -Force
        Write-Host "Removed the certificate from LocalMachine\$store"
      }
  }
}

Write-Host ''
if ($failed -gt 0) {
  Write-Host "$failed package(s) failed to uninstall." -ForegroundColor Red
} else {
  Write-Host 'Done.'
}
Read-Host 'Press Enter to close'
exit $failed
