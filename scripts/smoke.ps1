param([string]$Binary = (Join-Path $PSScriptRoot '..\dist\nullbot-drives-mcp.exe'))
$ErrorActionPreference = 'Stop'
$start = New-Object System.Diagnostics.ProcessStartInfo
$start.FileName = (Resolve-Path $Binary).Path
$start.Arguments = '--transport stdio'
$start.UseShellExecute = $false
$start.CreateNoWindow = $true
$start.RedirectStandardInput = $true
$start.RedirectStandardOutput = $true
$start.RedirectStandardError = $true
$start.EnvironmentVariables['NULLBOT_SERVICE_ACCOUNTS'] = '[]'
$process = New-Object System.Diagnostics.Process
$process.StartInfo = $start
function Send-Message($Value) {
    $process.StandardInput.WriteLine(($Value | ConvertTo-Json -Depth 10 -Compress))
    $process.StandardInput.Flush()
}
function Read-Reply([int]$ID) {
    $pending = $process.StandardOutput.ReadLineAsync()
    if (-not $pending.Wait(10000)) { throw "Timed out waiting for reply $ID" }
    if ($null -eq $pending.Result) { throw "Unexpected EOF waiting for reply $ID" }
    $reply = $pending.Result | ConvertFrom-Json
    if ($reply.id -ne $ID -or $reply.error) { throw "Invalid reply for request $ID" }
    return $reply.result
}
try {
    if (-not $process.Start()) { throw 'Unable to start server' }
    $stderr = $process.StandardError.ReadToEndAsync()
    Send-Message @{jsonrpc='2.0'; id=1; method='initialize'; params=@{protocolVersion='2024-11-05'; capabilities=@{}; clientInfo=@{name='offline-smoke'; version='1.0'}}}
    $init = Read-Reply 1
    if ($init.serverInfo.name -ne 'nullbot-drives-mcp') { throw 'Wrong server identity' }
    Send-Message @{jsonrpc='2.0'; method='notifications/initialized'; params=@{}}
    Send-Message @{jsonrpc='2.0'; id=2; method='tools/list'; params=@{}}
    $list = Read-Reply 2
    $names = @($list.tools | ForEach-Object { $_.name } | Sort-Object)
    $expected = @('drives_accounts','drives_download','drives_list','drives_read','drives_search')
    if ($names.Count -ne 5 -or (Compare-Object $expected $names)) { throw 'Unexpected tool list' }
    Send-Message @{jsonrpc='2.0'; id=3; method='tools/call'; params=@{name='drives_accounts'; arguments=@{}}}
    $accounts = Read-Reply 3
    if ($accounts.isError -or $accounts.content[0].text -ne '[]') { throw 'Expected empty accounts' }
    $process.StandardInput.Close()
    if (-not $process.WaitForExit(10000)) { throw 'Server did not exit after EOF' }
    if ($process.ExitCode -ne 0) { throw "Server exit code $($process.ExitCode)" }
    if ($stderr.Result.Length -ne 0) { throw 'Unexpected stderr from smoke server' }
    Write-Output "PASS: initialize ($($init.serverInfo.version)); tools/list: $($names -join ', '); accounts=[]; clean EOF exit"
} finally {
    if ($process.Id -and -not $process.HasExited) { $process.Kill(); $process.WaitForExit() }
    $process.Dispose()
}
