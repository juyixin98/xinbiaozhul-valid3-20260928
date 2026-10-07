# Local verification: build, run the test binary, then the sample job.
# Expected judgment:
#   - mink_tests prints one [TEST ...] PASS line per case and exits 0
#     with "[SUMMARY] ... failed=0".
#   - minkcli prints SUM / SUPPORT / ADDITIVITY lines; every ADDITIVITY
#     line must end with status=OK.
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")
& (Join-Path $PSScriptRoot "Build.ps1")
$runId = "local-$(Get-Date -Format yyyyMMdd-HHmmss)"
$tests = Get-ChildItem -Path build -Recurse -Filter mink_tests.exe |
         Select-Object -First 1
& $tests.FullName --run-id $runId
$cli = Get-ChildItem -Path build -Recurse -Filter minkcli.exe |
       Select-Object -First 1
& $cli.FullName fixtures/sample_job.txt
