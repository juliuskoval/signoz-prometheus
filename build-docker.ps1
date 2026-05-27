[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$Version,

    [Parameter(Position = 1)]
    [string]$Registry,

    [switch]$Push
)

$ErrorActionPreference = 'Stop'

$image = 'signoz-prometheus'
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

if ($Push -and -not $Registry) {
    throw '-Push requires -Registry (e.g. your Docker Hub username).'
}

$repo = if ($Registry) { "$Registry/$image" } else { $image }
$versionTag = "${repo}:${Version}"
$latestTag = "${repo}:latest"

docker build -t $versionTag -t $latestTag $scriptDir
if ($LASTEXITCODE -ne 0) { throw "docker build failed with exit code $LASTEXITCODE" }

if ($Push) {
    docker push $versionTag
    if ($LASTEXITCODE -ne 0) { throw "docker push $versionTag failed with exit code $LASTEXITCODE" }

    docker push $latestTag
    if ($LASTEXITCODE -ne 0) { throw "docker push $latestTag failed with exit code $LASTEXITCODE" }
}
