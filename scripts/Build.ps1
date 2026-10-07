# One-off configure + build. Does not run any tests.
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")
cmake -S . -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build --config Release
