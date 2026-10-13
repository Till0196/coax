@echo off
rem Copyright 2026 Till0196
rem SPDX-License-Identifier: Apache-2.0
rem
rem Assigns the in-box WinUSB driver to the devices listed in a coax INF.
rem Drag one or more *-winusb.inf onto this file, or double-click it to install
rem every INF sitting next to it. Elevates itself and asks for nothing else.
rem
rem The PowerShell half of this script lives below the marker line at the end
rem of the batch section; the batch half reads the file back and hands that
rem text to PowerShell.

setlocal EnableExtensions
set "COAX_WINUSB_SELF=%~f0"
set "COAX_WINUSB_INF="
for %%F in (%*) do call :append "%%~fF"
powershell -NoProfile -ExecutionPolicy Bypass -Command "$m='#@P'+'S'; $s=Get-Content -LiteralPath $env:COAX_WINUSB_SELF -Raw; Invoke-Expression (($s -split $m,2)[1])"
exit /b %ERRORLEVEL%

:append
set "COAX_WINUSB_INF=%COAX_WINUSB_INF%%~1|"
goto :eof

#@PS
$ErrorActionPreference = 'Stop'

function Quote([string]$s) { "'" + ($s -replace "'", "''") + "'" }

$self = $env:COAX_WINUSB_SELF
$here = Split-Path -Parent $self

$infs = @()
if ($env:COAX_WINUSB_INF) {
  $infs = @($env:COAX_WINUSB_INF -split '\|' | Where-Object { $_ })
}
if ($infs.Count -eq 0) {
  $infs = @(Get-ChildItem -LiteralPath $here -Filter '*-winusb.inf' | ForEach-Object { $_.FullName })
}
if ($infs.Count -eq 0) {
  Write-Host 'No INF found next to this script.' -ForegroundColor Red
  Read-Host 'Press Enter to close'
  exit 1
}
foreach ($inf in $infs) {
  if (-not (Test-Path -LiteralPath $inf -PathType Leaf) -or [IO.Path]::GetExtension($inf) -ne '.inf') {
    Write-Host "Not an INF file: $inf" -ForegroundColor Red
    Read-Host 'Press Enter to close'
    exit 1
  }
}

# Installing a driver needs an elevated token, and a dragged-and-dropped batch
# file never has one. Re-launch the same script under UAC and let the child do
# the work; the parent just exits.
$id = [Security.Principal.WindowsIdentity]::GetCurrent()
$isAdmin = ([Security.Principal.WindowsPrincipal]$id).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) {
  $joined = ($infs -join '|')
  $boot = '$m=' + (Quote ('#@P' + 'S')) + '; $s=Get-Content -LiteralPath $env:COAX_WINUSB_SELF -Raw; Invoke-Expression (($s -split $m,2)[1])'
  $cmd = '$env:COAX_WINUSB_SELF=' + (Quote $self) + '; $env:COAX_WINUSB_INF=' + (Quote $joined) + '; ' + $boot
  Start-Process -Verb RunAs -FilePath 'powershell.exe' `
    -ArgumentList '-NoProfile', '-ExecutionPolicy', 'Bypass', '-Command', $cmd | Out-Null
  exit 0
}

# A self-signed code-signing certificate of this run's own. Windows accepts a
# driver package whose catalog is signed by a certificate it trusts, so the
# certificate goes into Root and TrustedPublisher; its private key is deleted
# once the catalogs are signed, so nothing on this machine can sign anything
# else with it.
$subject = 'CN=coax WinUSB signing'
$cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject $subject `
  -CertStoreLocation Cert:\LocalMachine\My -NotAfter (Get-Date).AddYears(2)
Write-Host "Created certificate $($cert.Thumbprint)"
$signer = "Cert:\LocalMachine\My\$($cert.Thumbprint)"

try {
  $cer = Join-Path $env:TEMP ('coax-signing-' + $cert.Thumbprint + '.cer')
  Export-Certificate -Cert $cert -FilePath $cer | Out-Null
  try {
    foreach ($store in 'Root', 'TrustedPublisher') {
      $have = Get-ChildItem "Cert:\LocalMachine\$store" |
        Where-Object { $_.Thumbprint -eq $cert.Thumbprint }
      if (-not $have) {
        Import-Certificate -FilePath $cer -CertStoreLocation "Cert:\LocalMachine\$store" | Out-Null
        Write-Host "Trusted the certificate in LocalMachine\$store"
      }
    }
  } finally {
    Remove-Item -LiteralPath $cer -Force -ErrorAction SilentlyContinue
  }

  $failed = 0
  foreach ($inf in $infs) {
    $name = Split-Path -Leaf $inf
    Write-Host ''
    Write-Host "== $name" -ForegroundColor Cyan

    # The catalog name must match the INF's CatalogFile, and New-FileCatalog
    # hashes an entire directory, so each INF is staged alone.
    $hit = Select-String -LiteralPath $inf -Pattern '^\s*CatalogFile\s*=\s*(\S+)' | Select-Object -First 1
    if ($hit) { $cat = $hit.Matches[0].Groups[1].Value } else { $cat = [IO.Path]::GetFileNameWithoutExtension($inf) + '.cat' }

    $stage = Join-Path $env:TEMP ('coax-winusb-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $stage | Out-Null
    try {
      Copy-Item -LiteralPath $inf -Destination (Join-Path $stage $name)
      New-FileCatalog -Path $stage -CatalogFilePath (Join-Path $stage $cat) -CatalogVersion 2 | Out-Null
      $sig = Set-AuthenticodeSignature -FilePath (Join-Path $stage $cat) -Certificate $cert
      if ($sig.Status -ne 'Valid') {
        throw "Signing $cat failed: $($sig.StatusMessage)"
      }

      & pnputil.exe /add-driver (Join-Path $stage $name) /install
      $code = $LASTEXITCODE
      switch ($code) {
        0 { Write-Host "$name installed." -ForegroundColor Green }
        259 { Write-Host "$name added to the driver store, but no matching device is plugged in." -ForegroundColor Yellow }
        3010 { Write-Host "$name installed. Windows wants a reboot." -ForegroundColor Yellow }
        default {
          Write-Host "pnputil failed with exit code $code." -ForegroundColor Red
          $failed++
        }
      }
    } catch {
      Write-Host $_.Exception.Message -ForegroundColor Red
      $failed++
    } finally {
      Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    }
  }
} finally {
  # The public half stays in Root and TrustedPublisher for the packages it
  # signed; the private half goes.
  Remove-Item -LiteralPath $signer -DeleteKey -Force -ErrorAction SilentlyContinue
  if (Test-Path -LiteralPath $signer) {
    Write-Host "Could not delete the signing key $($cert.Thumbprint); remove it from LocalMachine\My by hand." -ForegroundColor Red
  }
}

Write-Host ''
if ($failed -gt 0) {
  Write-Host "$failed of $($infs.Count) INF(s) failed." -ForegroundColor Red
} else {
  Write-Host "Done. Unplug and replug the tuner if coax still does not see it."
}
Read-Host 'Press Enter to close'
exit $failed
