# Loop to create a pod every 10 seconds
for ($i = 1; $i -le 10; $i++) {
    $timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
    $podName = "pod-$timestamp"
    $namespace = "ns1"
    $image = "nginx"
    kubectl run $podName --image $image -n $namespace
    Write-Host "Created $podName"
    Start-Sleep -Seconds 10
}