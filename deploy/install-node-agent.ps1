<#
.SYNOPSIS
  Install the Nekomari agent on a Windows node, in one line.

.DESCRIPTION
  Mirrors deploy/install-node-agent.sh: it accepts the same agent flags the admin
  panel generates, so the command the panel shows works verbatim.

    iwr 'https://raw.githubusercontent.com/Aone2233/Nekomari/main/deploy/install-node-agent.ps1' -UseBasicParsing -OutFile install.ps1
    .\install.ps1 -e https://your-panel -t <token>

  Installer-only options (consumed here, never passed to the agent):
    -InstallDir          where to put the binary      (default $env:ProgramData\nekomari-agent)
    -InstallServiceName  scheduled task name          (default nekomari-agent)
    -InstallGhproxy      prefix for the download, for hosts that cannot reach GitHub
    -Version             pin a release instead of using the latest
    -Force               reinstall even if an agent is already running

  The token never ends up in the task. -t is accepted -- the panel hands out
  exactly that -- but the installer writes it to <InstallDir>\.agent-credentials,
  readable by SYSTEM and Administrators only, and starts the agent with
  --token-file. Otherwise the node's whole identity sits in the scheduled task's
  command line, where `Get-ScheduledTask` or `schtasks /query /v` shows it.

  Note on service type: this registers a Scheduled Task rather than a Windows
  Service. The agent is a console program that holds a WebSocket open; a task with
  "run whether user is logged on or not" gives the same always-on behaviour without
  needing a service wrapper binary.
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][Alias('e')][string]$Endpoint,
  [Parameter(Mandatory = $true)][Alias('t')][string]$Token,
  [string]$InstallDir = "$env:ProgramData\nekomari-agent",
  [string]$InstallServiceName = "nekomari-agent",
  [string]$InstallGhproxy = "",
  [string]$Version = "",
  [switch]$Force,
  [switch]$DisableWebSsh,
  [switch]$DisableAutoUpdate,
  [switch]$IgnoreUnsafeCert,
  # The admin panel generates GNU-style flags (--disable-web-ssh) because the same
  # command shape is used for the Linux and macOS one-liners. PowerShell parameters
  # cannot be named like that, so anything unrecognised lands here and is translated
  # below -- which is what lets the panel's command work verbatim on Windows too.
  [Parameter(ValueFromRemainingArguments = $true)][string[]]$AgentFlags
)

$ErrorActionPreference = "Stop"
$Repo = "Aone2233/Nekomari"

# Translate the GNU-style agent flags into the switches above.
#
# Both spellings are handled: --flag=value and the space-separated --flag value the
# admin panel emits. The space-separated form used to fall through to the
# "ignoring unrecognised argument" branch, so a custom install directory, service
# name, ghproxy or pinned version was silently dropped on Windows while the same
# command worked on Linux.
$flags = @($AgentFlags)
for ($i = 0; $i -lt $flags.Count; $i++) {
  $f = $flags[$i]
  $next = $null
  if ($i + 1 -lt $flags.Count) { $next = $flags[$i + 1] }
  switch -Regex ($f) {
    '^--disable-web-ssh$'     { $DisableWebSsh = $true }
    '^--disable-auto-update$' { $DisableAutoUpdate = $true }
    '^--ignore-unsafe-cert$'  { $IgnoreUnsafeCert = $true }
    '^--force$'               { $Force = $true }
    '^--install-dir=(.+)$'    { $InstallDir = $Matches[1] }
    '^--install-dir$'         { if ($next) { $InstallDir = $next; $i++ } }
    '^--install-service-name=(.+)$' { $InstallServiceName = $Matches[1] }
    '^--install-service-name$'      { if ($next) { $InstallServiceName = $next; $i++ } }
    '^--install-ghproxy=(.*)$'      { $InstallGhproxy = $Matches[1] }
    '^--install-ghproxy$'           { if ($next) { $InstallGhproxy = $next; $i++ } }
    '^--version=(.+)$'        { $Version = $Matches[1] }
    '^--version$'             { if ($next) { $Version = $next; $i++ } }
    default {
      if ($f) { Write-Host "  !! ignoring unrecognised argument '$f'" -ForegroundColor Yellow }
    }
  }
}

function Log  { param($m) Write-Host "  $m" }
function Die  { param($m) Write-Host "  !! $m" -ForegroundColor Red; exit 1 }

# --- architecture -----------------------------------------------------------
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64" }
  "ARM64" { "arm64" }
  default { Die "unsupported architecture $env:PROCESSOR_ARCHITECTURE" }
}
$asset = "komari-agent-windows-$arch.exe"
Log "platform windows/$arch"

# --- already installed? -----------------------------------------------------
if (-not $Force) {
  $existing = Get-ScheduledTask -TaskName $InstallServiceName -ErrorAction SilentlyContinue
  if ($existing -and $existing.State -eq "Running") {
    Die "$InstallServiceName is already running; pass -Force to replace it"
  }
}

# --- version ----------------------------------------------------------------
if (-not $Version) {
  try {
    $Version = (Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing).tag_name
  } catch {
    Die "could not determine the latest release; pass -Version vX.Y.Z"
  }
}
if (-not $Version) { Die "could not determine the latest release; pass -Version vX.Y.Z" }
Log "version $Version"

$base = "https://github.com/$Repo/releases/download/$Version"
if ($InstallGhproxy) { $base = "$($InstallGhproxy.TrimEnd('/'))/$($base -replace '^https://','')" }

# --- download + verify ------------------------------------------------------
$tmp = Join-Path $env:TEMP ("nekomari-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
  Log "downloading $asset"
  $binPath = Join-Path $tmp $asset
  $sumsPath = Join-Path $tmp "SHA256SUMS.txt"
  try {
    Invoke-WebRequest "$base/$asset" -OutFile $binPath -UseBasicParsing
    Invoke-WebRequest "$base/SHA256SUMS.txt" -OutFile $sumsPath -UseBasicParsing
  } catch {
    Die "download failed from $base : $($_.Exception.Message)"
  }

  $want = (Select-String -Path $sumsPath -Pattern ([regex]::Escape($asset) + '\s*$') |
           Select-Object -First 1).Line -split '\s+' | Select-Object -First 1
  if (-not $want) { Die "$asset is not listed in SHA256SUMS.txt" }
  $got = (Get-FileHash -Path $binPath -Algorithm SHA256).Hash.ToLower()
  if ($want.ToLower() -ne $got) { Die "checksum mismatch for $asset`n     want $want`n     got  $got" }
  Log "checksum verified"

  # --- install --------------------------------------------------------------
  New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
  $target = Join-Path $InstallDir $asset
  Copy-Item $binPath $target -Force
  Log "installed to $target"

  # --- credential -----------------------------------------------------------
  # The token must not go into the task's arguments: a scheduled task's command
  # line is visible to anyone who can run `schtasks /query /v` or Get-ScheduledTask,
  # the same way `-t` is visible in `ps` on Linux. It goes into a file the task's
  # account (SYSTEM) alone can read, and the agent is started with --token-file.
  # See docs/SECRETS.md.
  $tokenFile = Join-Path $InstallDir ".agent-credentials"
  Set-Content -Path $tokenFile -Value "AGENT_TOKEN=$Token" -NoNewline -Encoding ascii
  & icacls.exe $tokenFile /inheritance:r /grant:r "SYSTEM:(F)" "Administrators:(F)" | Out-Null
  if ($LASTEXITCODE -ne 0) { Die "could not restrict access to $tokenFile" }
  Log "credential written: $tokenFile (SYSTEM and Administrators only)"

  $agentArgs = @("-e", $Endpoint, "--token-file", $tokenFile)
  if ($DisableWebSsh)     { $agentArgs += "--disable-web-ssh" }
  if ($DisableAutoUpdate) { $agentArgs += "--disable-auto-update" }
  if ($IgnoreUnsafeCert)  { $agentArgs += "--ignore-unsafe-cert" }

  # --- scheduled task -------------------------------------------------------
  $action = New-ScheduledTaskAction -Execute $target -Argument ($agentArgs -join ' ') -WorkingDirectory $InstallDir
  $trigger = New-ScheduledTaskTrigger -AtStartup
  $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries `
    -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero)
  $principal = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest

  Unregister-ScheduledTask -TaskName $InstallServiceName -Confirm:$false -ErrorAction SilentlyContinue
  Register-ScheduledTask -TaskName $InstallServiceName -Action $action -Trigger $trigger `
    -Settings $settings -Principal $principal | Out-Null
  Start-ScheduledTask -TaskName $InstallServiceName
  Start-Sleep -Seconds 3

  $state = (Get-ScheduledTask -TaskName $InstallServiceName).State
  Log "scheduled task ${InstallServiceName}: $state"
  if ($state -ne "Running") { Die "task did not start; check Task Scheduler history for '$InstallServiceName'" }
} finally {
  Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host ""
Log "done. The node should appear in the panel within a minute."
