<#
.SYNOPSIS
    This script provides functions for Kubernetes code-generation tools, translated from the original bash script.

.DESCRIPTION
    This presents several functions for packages which want to use kubernetes
    code-generation tools.

    These functions insist that your input IDL (commented go) files be located in
    go packages following the pattern $input_pkg_root/$something_sans_slash/$api_version .
    Those $something_sans_slash will be propagated into the output directory structure.

    To use, dot-source this script in your PowerShell session:
    . .\kube-codegen.ps1

    Then, you can call the exported functions, e.g.:
    Invoke-KubeCodegenHelpers -InputDir "path/to/pkg"

.LICENSE
    Copyright 2023 The Kubernetes Authors.

    Licensed under the Apache License, Version 2.0 (the "License");
    you may not use this file except in compliance with the License.
    You may obtain a copy of the License at

         http://www.apache.org/licenses/LICENSE-2.0

    Unless required by applicable law or agreed to in writing, software
    distributed under the License is distributed on an "AS IS" BASIS,
    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
    See the License for the specific language governing permissions and
    limitations under the License.
#>

# Stop on errors and undefined variables
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# --- Script Setup ---

# Get the script's root directory
$KUBE_CODEGEN_ROOT = $PSScriptRoot

# Callers which want a specific tag of the k8s.io/code-generator repo should
# set the $env:KUBE_CODEGEN_TAG to the tag name, e.g. $env:KUBE_CODEGEN_TAG="release-1.32"
# before sourcing this file.
$CODEGEN_VERSION_SPEC = if (-not [string]::IsNullOrEmpty($env:KUBE_CODEGEN_TAG)) {
    "@$($env:KUBE_CODEGEN_TAG)"
}
else {
    ""
}

# Go installs in $GOBIN if defined, and $GOPATH/bin otherwise.
function Get-Gobin {
    $from_env = $(go env GOBIN)
    if (-not [string]::IsNullOrEmpty($from_env)) {
        return $from_env
    }
    else {
        return (Join-Path $(go env GOPATH) "bin")
    }
}
$script:GOBIN = Get-Gobin
$env:GOBIN = $script:GOBIN # Export it for child processes

# Set GO111MODULE for all 'go' commands in this script
$env:GO111MODULE = 'on'

# --- Internal Helper Functions ---

<#
.SYNOPSIS
    Finds Go packages containing a specific codegen tag.
#>
function Get-PackagesWithTag {
    param(
        [string]$BaseDir,
        [string]$Tag
    )
    
    # Find all unique directories containing the tag
    $dirs = Get-ChildItem -Path $BaseDir -Filter "*.go" -Recurse -File |
    Select-String -Pattern "^\s*//\s*$Tag" |
    ForEach-Object { Split-Path -Path $_.Path } |
    Sort-Object -Unique
   
    $input_pkgs = @()
    foreach ($dir in $dirs) {
        try {
            Push-Location -Path $dir
            $pkg = $(go list -find .)
            $input_pkgs += $pkg
        }
        catch {
            Write-Warning "Could not 'go list' in directory $dir. Error: $_"
        }
        finally {
            Pop-Location
        }
    }
    Write-Host $input_pkgs.Length
    return $input_pkgs
}

<#
.SYNOPSIS
    Removes generated Go files based on a marker comment.
#>
function Remove-GeneratedFilesByComment {
    param(
        [string]$BaseDir,
        [string]$Pattern
    )
    
    if (-not (Test-Path $BaseDir)) { return }

    Get-ChildItem -Path $BaseDir -Filter "*.go" -Recurse -File |
    Select-String -Pattern $Pattern |
    Select-Object -ExpandProperty Path |
    Remove-Item -Force -ErrorAction SilentlyContinue
}

<#
.SYNOPSIS
    Removes generated Go files based on a filename pattern.
#>
function Remove-GeneratedFilesByName {
    param(
        [string]$BaseDir,
        [string]$Name
    )
    
    if (-not (Test-Path $BaseDir)) { return }

    Get-ChildItem -Path $BaseDir -Filter $Name -Recurse -File |
    Remove-Item -Force -ErrorAction SilentlyContinue
}


# --- Public Functions ---

<#
.SYNOPSIS
    Generate tagged helper code: conversions, deepcopy, defaults and validations
#>
function Invoke-KubeCodegenHelpers {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true, Position = 0)]
        [string]$InputDir,

        [string]$Boilerplate = (Join-Path $KUBE_CODEGEN_ROOT "hack/boiler-plate.go.txt"),

        [string[]]$ExtraPeerDir = @()
    )

    $v = $env:KUBE_VERBOSE | Out-String

    if ($v -eq "") { $v = 5 }

    # Install binaries
    try {
        Push-Location -Path $KUBE_CODEGEN_ROOT
        $BINS = @(
            "conversion-gen$CODEGEN_VERSION_SPEC",
            "deepcopy-gen$CODEGEN_VERSION_SPEC",
            "defaulter-gen$CODEGEN_VERSION_SPEC",
            "validation-gen$CODEGEN_VERSION_SPEC"
        )
        $InstallTargets = $BINS | ForEach-Object { "k8s.io/code-generator/cmd/$_" }
        go install $InstallTargets
    }
    finally {
        Pop-Location
    }

    # Deepcopy
    $input_pkgs = Get-PackagesWithTag -BaseDir $InputDir -Tag "\+k8s:deepcopy-gen="
    Write-Host $input_pkgs.Length "input_pkgs"
    if ($input_pkgs.Count -gt 0) {
        Write-Output "Generating deepcopy code for $($input_pkgs.Count) targets"
        Remove-GeneratedFilesByName -BaseDir $InputDir -Name "zz_generated.deepcopy.go"
        
        $ExePath = Join-Path $script:GOBIN "deepcopy-gen"
        & $ExePath -v $v --output-file "zz_generated.deepcopy.go" --go-header-file $Boilerplate @input_pkgs
    }

    # Validations
    $input_pkgs = Get-PackagesWithTag -BaseDir $InputDir -Tag "\+k8s:validation-gen="
    if ($input_pkgs.Count -gt 0) {
        Write-Output "Generating validation code for $($input_pkgs.Count) targets"
        Remove-GeneratedFilesByName -BaseDir $InputDir -Name "zz_generated.validations.go"
        
        $ExePath = Join-Path $script:GOBIN "validation-gen"
        & $ExePath -v $v --output-file "zz_generated.validations.go" --go-header-file $Boilerplate @input_pkgs
    }

    # Defaults
    $input_pkgs = Get-PackagesWithTag -BaseDir $InputDir -Tag "\+k8s:defaulter-gen="
    if ($input_pkgs.Count -gt 0) {
        Write-Output "Generating defaulter code for $($input_pkgs.Count) targets"
        Remove-GeneratedFilesByName -BaseDir $InputDir -Name "zz_generated.defaults.go"
        
        $ExePath = Join-Path $script:GOBIN "defaulter-gen"
        & $ExePath -v $v --output-file "zz_generated.defaults.go" --go-header-file $Boilerplate @input_pkgs
    }

    # Conversions
    $input_pkgs = Get-PackagesWithTag -BaseDir $InputDir -Tag "\+k8s:conversion-gen="
    if ($input_pkgs.Count -gt 0) {
        Write-Output "Generating conversion code for $($input_pkgs.Count) targets"
        Remove-GeneratedFilesByName -BaseDir $InputDir -Name "zz_generated.conversion.go"
        
        $extra_peer_args = @()
        foreach ($arg in $ExtraPeerDir) {
            $extra_peer_args += "--extra-peer-dirs", $arg
        }
        
        $ExePath = Join-Path $script:GOBIN "conversion-gen"
        & $ExePath -v $v --output-file "zz_generated.conversion.go" --go-header-file $Boilerplate @extra_peer_args @input_pkgs
    }
}

<#
.SYNOPSIS
    Generate openapi code
#>
function Invoke-KubeCodegenOpenAPI {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true, Position = 0)]
        [string]$InputDir,

        [Parameter(Mandatory = $true)]
        [string]$OutputDir,

        [Parameter(Mandatory = $true)]
        [string]$OutputPkg,

        [string[]]$ExtraPkgs = @(),
        [string]$ReportFilename = "/dev/null",
        [switch]$UpdateReport,
        [string]$Boilerplate = (Join-Path $KUBE_CODEGEN_ROOT "hack/boilerplate.go.txt")
    )

    $v = $env:KUBE_VERBOSE | Out-String 
    
    # Handle /dev/null vs NUL
    if ($ReportFilename -eq "/dev/null" -and $IsWindows) {
        $ReportFilename = "NUL" 
    }
    
    $new_report = ""
    $tempFile = $null
    if ($UpdateReport.IsPresent) {
        $new_report = $ReportFilename
    }
    else {
        $tempFile = [System.IO.Path]::GetTempFileName()
        $new_report = $tempFile
    }

    # Install binaries
    try {
        Push-Location -Path $KUBE_CODEGEN_ROOT
        $BINS = @(
            "openapi-gen$CODEGEN_VERSION_SPEC"
        )
        $InstallTargets = $BINS | ForEach-Object { "k8s.io/kube-openapi/cmd/$_" }
        go install $InstallTargets
    }
    finally {
        Pop-Location
    }

    $input_pkgs = @() + $ExtraPkgs
    $input_pkgs += Get-PackagesWithTag -BaseDir $InputDir -Tag "\+k8s:openapi-gen="

    if ($input_pkgs.Count -gt $ExtraPkgs.Count) {
        # Check if we found any new ones
        Write-Output "Generating openapi code for $($input_pkgs.Count) targets"
        
        # The original script finds this in $in_dir, which seems odd, but we follow it.
        Remove-GeneratedFilesByName -BaseDir $InputDir -Name "zz_generated.openapi.go" 

        $ExePath = Join-Path $script:GOBIN "openapi-gen"
        & $ExePath -v $v `
            --output-file "zz_generated.openapi.go" `
            --go-header-file $Boilerplate `
            --output-dir $OutputDir `
            --output-pkg $OutputPkg `
            --report-filename $new_report `
            "k8s.io/apimachinery/pkg/apis/meta/v1" `
            "k8s.io/apimachinery/pkg/runtime" `
            "k8s.io/apimachinery/pkg/version" `
            @input_pkgs
    }

    if (-not $UpdateReport.IsPresent) {
        if (-not (Test-Path $ReportFilename)) {
            New-Item -Path $ReportFilename -ItemType File -ErrorAction SilentlyContinue | Out-Null # touch
        }
        
        $diff = Compare-Object -ReferenceObject $(Get-Content $ReportFilename) -DifferenceObject $(Get-Content $new_report)
        if ($diff) {
            Write-Error "ERROR:"
            Write-Error "`tAPI rule check failed for ${ReportFilename}: new reported violations"
            Write-Error "`tPlease read api/api-rules/README.md"
            if ($tempFile) { Remove-Item $tempFile -ErrorAction SilentlyContinue }
            throw "API rule check failed"
        }
        if ($tempFile) { Remove-Item $tempFile -ErrorAction SilentlyContinue }
    }
}

<#
.SYNOPSIS
    Generate client code
#>
function Invoke-KubeCodegenClient {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true, Position = 0)]
        [string]$InputDir,

        [Parameter(Mandatory = $true)]
        [string]$OutputDir,

        [Parameter(Mandatory = $true)]
        [string]$OutputPkg,

        [string]$OneInputApi,
        [string]$Boilerplate = (Join-Path $KUBE_CODEGEN_ROOT "hack/boilerplate.go.txt"),
        [string]$ClientsetName = "clientset",
        [string]$VersionedName = "versioned",
        [switch]$WithApplyConfig,
        [string]$ApplyConfigName = "applyconfiguration",
        [string]$ApplyConfigExternals = "",
        [string]$ApplyConfigOpenAPISchema = "",
        [switch]$WithWatch,
        [string]$ListersName = "listers",
        [string]$InformersName = "informers",
        [string]$PluralExceptions = "",
        [switch]$PrefersProtobuf
    )

    $v = $env:KUBE_VERBOSE | Out-String 
    $one_input_api_path = ""
    if (-not [string]::IsNullOrEmpty($OneInputApi)) {
        $one_input_api_path = "/$OneInputApi" # Original script prepends '/'
    }

    # Ensure output dir exists
    New-Item -Path $OutputDir -ItemType Directory -ErrorAction SilentlyContinue | Out-Null

    # Install binaries
    try {
        Push-Location -Path $KUBE_CODEGEN_ROOT
        $BINS = @(
            "applyconfiguration-gen$CODEGEN_VERSION_SPEC",
            "client-gen$CODEGEN_VERSION_SPEC",
            "informer-gen$CODEGEN_VERSION_SPEC",
            "lister-gen$CODEGEN_VERSION_SPEC"
        )
        $InstallTargets = $BINS | ForEach-Object { "k8s.io/code-generator/cmd/$_" }
        go install $InstallTargets
    }
    finally {
        Pop-Location
    }

    # Find group versions
    $group_versions = @()
    $input_pkgs = @()
    
    $searchPath = "$InputDir$one_input_api_path"
    
    $dirs = Get-ChildItem -Path $searchPath -Filter "*.go" -Recurse -File |
    Select-String -Pattern "^\s*//\s*\+genclient" |
    ForEach-Object { Split-Path -Path $_.Path } |
    Sort-Object -Unique
    
    foreach ($dir in $dirs) {
        try {
            $pkg = ""
            Push-Location -Path $dir
            try {
                $pkg = $(go list -find .)
            }
            finally {
                Pop-Location
            }
            
            $leaf = Split-Path -Path $dir -Leaf
            if ($leaf -match '^v[0-9]+((alpha|beta)[0-9]+)?$') {
                $input_pkgs += $pkg
                
                $dir2 = Split-Path -Path $dir -Parent
                $leaf2 = Split-Path -Path $dir2 -Leaf
                $group_versions += "$leaf2/$leaf"
            }
        }
        catch {
            Write-Warning "Could not process directory $dir. Error: $_"
        }
    }

    if ($group_versions.Count -eq 0) {
        Write-Output "No group versions found. Exiting."
        return
    }
    
    $applyconfig_pkg = ""
    if ($WithApplyConfig.IsPresent) {
        $applyconfig_pkg = "$OutputPkg/$ApplyConfigName"
        $applyconfig_dir = Join-Path $OutputDir $ApplyConfigName
        
        Write-Output "Generating applyconfig code for $($input_pkgs.Count) targets"
        
        Remove-GeneratedFilesByComment -BaseDir $applyconfig_dir -Pattern '^// Code generated by applyconfiguration-gen. DO NOT EDIT.$'

        $ExePath = Join-Path $script:GOBIN "applyconfiguration-gen"
        & $ExePath -v $v `
            --go-header-file $Boilerplate `
            --output-dir $applyconfig_dir `
            --output-pkg $applyconfig_pkg `
            --external-applyconfigurations $ApplyConfigExternals `
            --openapi-schema $ApplyConfigOpenAPISchema `
            @input_pkgs
    }
    
    Write-Output "Generating client code for $($group_versions.Count) targets"
    $clientset_dir = Join-Path $OutputDir $ClientsetName
    
    Remove-GeneratedFilesByComment -BaseDir $clientset_dir -Pattern '^// Code generated by client-gen. DO NOT EDIT.$'
    
    $inputs_args = @()
    foreach ($arg in $group_versions) {
        $inputs_args += "--input", $arg
    }
    
    # Get absolute path for input-base
    $input_base_abs = (Resolve-Path -Path $InputDir).Path
    
    $ExePath = Join-Path $script:GOBIN "client-gen"
    & $ExePath -v $v `
        --go-header-file $Boilerplate `
        --output-dir $clientset_dir `
        --output-pkg "$OutputPkg/$ClientsetName" `
        --clientset-name $VersionedName `
        --apply-configuration-package $applyconfig_pkg `
        --input-base $input_base_abs `
        --plural-exceptions $PluralExceptions `
        --prefers-protobuf="$($PrefersProtobuf.IsPresent.ToString().ToLower())" `
        @inputs_args

    if ($WithWatch.IsPresent) {
        $listers_dir = Join-Path $OutputDir $ListersName
        $listers_pkg = "$OutputPkg/$ListersName"
        
        Write-Output "Generating lister code for $($input_pkgs.Count) targets"
        Remove-GeneratedFilesByComment -BaseDir $listers_dir -Pattern '^// Code generated by lister-gen. DO NOT EDIT.$'
        
        $ExePath = Join-Path $script:GOBIN "lister-gen"
        & $ExePath -v $v `
            --go-header-file $Boilerplate `
            --output-dir $listers_dir `
            --output-pkg $listers_pkg `
            --plural-exceptions $PluralExceptions `
            @input_pkgs

        $informers_dir = Join-Path $OutputDir $InformersName
        $informers_pkg = "$OutputPkg/$InformersName"
        
        Write-Output "Generating informer code for $($input_pkgs.Count) targets"
        Remove-GeneratedFilesByComment -BaseDir $informers_dir -Pattern '^// Code generated by informer-gen. DO NOT EDIT.$'

        $ExePath = Join-Path $script:GOBIN "informer-gen"
        & $ExePath -v $v `
            --go-header-file $Boilerplate `
            --output-dir $informers_dir `
            --output-pkg $informers_pkg `
            --versioned-clientset-package "$OutputPkg/$ClientsetName/$VersionedName" `
            --listers-package $listers_pkg `
            --plural-exceptions $PluralExceptions `
            @input_pkgs
    }
}

<#
.SYNOPSIS
    Generate register code
#>
function Invoke-KubeCodegenRegister {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true, Position = 0)]
        [string]$InputDir,

        [string]$Boilerplate = (Join-Path $KUBE_CODEGEN_ROOT "hack/boilerplate.go.txt")
    )

    $v = $env:KUBE_VERBOSE | Out-String 

    # Install binaries
    try {
        Push-Location -Path $KUBE_CODEGEN_ROOT
        $BINS = @(
            "register-gen$CODEGEN_VERSION_SPEC"
        )
        $InstallTargets = $BINS | ForEach-Object { "k8s.io/code-generator/cmd/$_" }
        go install $InstallTargets
    }
    finally {
        Pop-Location
    }

    # Register
    $input_pkgs = Get-PackagesWithTag -BaseDir $InputDir -Tag "\+groupName"
    if ($input_pkgs.Count -gt 0) {
        Write-Output "Generating register code for $($input_pkgs.Count) targets"
        
        Remove-GeneratedFilesByName -BaseDir $InputDir -Name "zz_generated.register.go"
        
        $ExePath = Join-Path $script:GOBIN "register-gen"
        & $ExePath -v $v --output-file "zz_generated.register.go" --go-header-file $Boilerplate @input_pkgs
    }
}

# Export the public functions so they are available after dot-sourcing
# Export-ModuleMember -Function Invoke-KubeCodegenHelpers, Invoke-KubeCodegenOpenAPI, Invoke-KubeCodegenClient, Invoke-KubeCodegenRegister