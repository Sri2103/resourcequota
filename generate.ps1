param (
    [ValidateSet("deepcopy", "client", "lister", "informer")]
    [string]$Run,

    [string]$ModulePath = "github.com/sri2103/resource-quota-enforcer",
    [string]$ApiGroup = "platform",
    [string]$ApiVersion = "v1alpha1",
    [string]$HeaderFile = "./hack/boiler-plate.go.txt",
    [string]$VerboseLevel = "5"
)

$InputDir = "$ModulePath/pkg/apis/$ApiGroup/$ApiVersion"
$BasePackage = "$ModulePath/pkg/generated"

function Run-DeepcopyGen {
    Write-Host "`n🔁 Running deepcopy-gen..." -ForegroundColor Cyan
    & deepcopy-gen.exe `
        --input-dirs=$InputDir `
        --output-package="$BasePackage/deepcopy" `
        --go-header-file=$HeaderFile `
        -v $VerboseLevel
}

function Run-ClientGen {
    Write-Host "`n🔁 Running client-gen..." -ForegroundColor Cyan
    & client-gen.exe `
        --input-dirs=$InputDir `
        --output-package="$BasePackage/clientset" `
        --go-header-file=$HeaderFile `
        -v $VerboseLevel
}

function Run-ListerGen {
    Write-Host "`n🔁 Running lister-gen..." -ForegroundColor Cyan
    & lister-gen.exe `
        --input-dirs=$InputDir `
        --output-package="$BasePackage/listers" `
        --go-header-file=$HeaderFile `
        -v $VerboseLevel
}

function Run-InformerGen {
    Write-Host "`n🔁 Running informer-gen..." -ForegroundColor Cyan
    & informer-gen.exe `
        --input-dirs=$InputDir `
        --versioned-clientset-package="$BasePackage/clientset/versioned" `
        --listers-package="$BasePackage/listers" `
        --output-package="$BasePackage/informers" `
        --go-header-file=$HeaderFile `
        -v $VerboseLevel
}

switch ($Run) {
    "deepcopy" { Run-DeepcopyGen }
    "client"   { Run-ClientGen }
    "lister"   { Run-ListerGen }
    "informer" { Run-InformerGen }
    default    { Write-Host "❌ Please specify -Run with one of: deepcopy, client, lister, informer" -ForegroundColor Red }
}
