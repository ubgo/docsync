# windows-smoke.ps1 <path to ds.exe>
#
# Drives a real ds.exe through the flow a user follows, on Windows, in a
# throwaway git repository: init, def, scan, check, a change, the finding, an
# ack, and the things most likely to differ on this platform -- path
# separators, CRLF line endings, the .ds lock taken by two commands at once,
# and a resolver plugin run as a child process.
#
# Why it exists: the Windows build was cross-compiled and shipped without ever
# being started on Windows. Compiling is not running. This is the check that
# turns "builds for Windows" into "works on Windows", and it is what
# .github/workflows/windows.yml runs against both a build from source and the
# released archive.
#
# Each case prints PASS or FAIL; the script exits 1 if any failed.
param([Parameter(Mandatory = $true)][string]$Ds)

$ErrorActionPreference = "Stop"
$Ds = (Resolve-Path $Ds).Path
$script:pass = 0
$script:fail = 0

function Check([string]$name, [bool]$ok, [string]$detail = "") {
  if ($ok) { Write-Host "  PASS  $name"; $script:pass++ }
  else { Write-Host "  FAIL  $name $detail"; $script:fail++ }
}

# Run ds and return its combined output and exit code. Native-command errors
# must not stop the script: a non-zero exit is often the thing under test.
function Run([string[]]$argv) {
  $old = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  try {
    $out = & $Ds @argv 2>&1 | Out-String
    return @{ out = $out; code = $LASTEXITCODE }
  } finally { $ErrorActionPreference = $old }
}

function WriteLF([string]$path, [string]$text) {
  [System.IO.File]::WriteAllText((Join-Path (Get-Location) $path), $text.Replace("`r`n", "`n"))
}

$root = Join-Path ([System.IO.Path]::GetTempPath()) ("ds-smoke-" + [System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $root | Out-Null
Push-Location $root
try {
  git init -q . | Out-Null
  git config user.email t@t
  git config user.name t
  git config core.autocrlf false
  New-Item -ItemType Directory -Path docs, src | Out-Null
  WriteLF "src/a.go" "package s`n`n// Depth is the limit.`nfunc Depth() int {`n`treturn 12`n}`n"
  WriteLF "src/conf.yaml" "port: 8080`n"

  $r = Run @("version")
  Check "ds version runs" ($r.code -eq 0 -and $r.out -match "^ds ") $r.out

  $r = Run @("init")
  Check "init creates .ds" ($r.code -eq 0 -and (Test-Path ".ds/config.toml")) $r.out

  # A def by symbol through the tree-sitter tier, and one by line in YAML.
  $r = Run @("def", "src/a.go#Depth")
  $id = ($r.out.Trim() -split "`n")[-1].Trim()
  Check "def mints an id for a Go symbol" ($r.code -eq 0 -and $id -match "^[a-z0-9-]+$") $r.out
  Check "def wrote the directive into the file" ((Get-Content src/a.go -Raw) -match "ds:def id=$id")

  $r = Run @("def", "src/conf.yaml:1")
  $cfg = ($r.out.Trim() -split "`n")[-1].Trim()
  Check "def mints an id for a YAML line" ($r.code -eq 0 -and $cfg -match "^[a-z0-9-]+$") $r.out

  WriteLF "docs/p.md" "# P`n`nDepth is [twelve](ds:block?id=$id) and the port is [8080](ds:cfg?id=$cfg).`n"

  $r = Run @("scan")
  Check "scan succeeds" ($r.code -eq 0) $r.out
  $ledger = Get-Content .ds/ledger.tsv -Raw
  # Paths are repository-relative with forward slashes on every platform: a
  # ledger holding src\a.go would not match the same repo scanned on Linux.
  Check "the ledger records src/a.go with forward slashes" ($ledger -match "src/a\.go" -and $ledger -notmatch "src\\a\.go")
  Check "the ledger has LF line endings" ($ledger -notmatch "`r`n")

  git add -A | Out-Null
  git commit -q -m a | Out-Null

  $r = Run @("check")
  Check "check is clean on an unchanged tree" ($r.code -eq 0) $r.out

  # A path argument with backslashes, as a Windows user types it.
  $r = Run @("render", "docs\p.md")
  Check "render accepts a backslash path" ($r.code -eq 0 -and $r.out -match "8080") $r.out

  $r = Run @("locate", $id)
  Check "locate prints a forward-slash path" ($r.code -eq 0 -and $r.out -match "src/a\.go") $r.out

  # The change a doc should hear about.
  WriteLF "src/a.go" ((Get-Content src/a.go -Raw).Replace("return 12", "return 6"))
  $r = Run @("check")
  Check "a changed block fails check" ($r.code -eq 1 -and $r.out -match $id) $r.out

  $r = Run @("ack", $id, "--doc", "docs/p.md", "--line", "3", "--note", "still true")
  Check "ack records the review" ($r.code -eq 0) $r.out
  $r = Run @("check")
  Check "check is clean after the ack" ($r.code -eq 0) $r.out

  # CRLF: a file saved with Windows line endings is the same block.
  $lf = Get-Content src/a.go -Raw
  [System.IO.File]::WriteAllText((Join-Path (Get-Location) "src/a.go"), $lf.Replace("`r`n", "`n").Replace("`n", "`r`n"))
  $r = Run @("check")
  Check "converting a file to CRLF is not a change" ($r.code -eq 0) $r.out

  # The .ds lock, which on Windows is its own implementation: several scans
  # at once must all succeed and leave a ledger that still parses.
  $jobs = 1..4 | ForEach-Object { Start-Job -ScriptBlock { param($ds, $dir) Set-Location $dir; & $ds scan 2>&1 | Out-Null; $LASTEXITCODE } -ArgumentList $Ds, $root }
  $codes = $jobs | Wait-Job | Receive-Job
  $jobs | Remove-Job
  Check "four concurrent scans all succeed" (($codes | Where-Object { $_ -ne 0 }).Count -eq 0) ("exit codes: " + ($codes -join ","))
  $r = Run @("check")
  Check "the ledger is intact after concurrent scans" ($r.code -eq 0) $r.out

  $r = Run @("doctor")
  Check "doctor reports no FAIL" ($r.code -eq 0 -and $r.out -notmatch "FAIL") $r.out

  $r = Run @("check", "--json")
  $json = $null
  try { $json = $r.out | ConvertFrom-Json } catch { }
  Check "check --json is valid JSON" ($null -ne $json -and $json.json_format -eq 1) $r.out

  # MCP over stdio: one request in, one response out.
  $req = '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}'
  $old = $ErrorActionPreference; $ErrorActionPreference = "Continue"
  $resp = $req | & $Ds mcp 2>$null | Out-String
  $ErrorActionPreference = $old
  Check "ds mcp answers initialize over stdio" ($resp -match '"result"') $resp

  # A resolver plugin beside ds.exe, run as a child process.
  $plugin = Join-Path (Split-Path $Ds) "ds-resolve-vault.exe"
  if (Test-Path $plugin) {
    $old = $ErrorActionPreference; $ErrorActionPreference = "Continue"
    $pout = '{"addr":"x","want":"exists"}' | & $plugin 2>&1 | Out-String
    $ErrorActionPreference = $old
    Check "a resolver plugin starts and answers in JSON" ($pout.Trim().StartsWith("{")) $pout
  } else {
    Write-Host "  SKIP  no ds-resolve-vault.exe beside ds.exe"
  }
} finally {
  Pop-Location
  Remove-Item -Recurse -Force $root -ErrorAction SilentlyContinue
}

Write-Host ""
Write-Host "  ---- $script:pass passed, $script:fail failed ----"
if ($script:fail -gt 0) { exit 1 }
