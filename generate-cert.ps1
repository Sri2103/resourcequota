# ===============================
# PowerShell Script: generate-webhook-certs-and-yaml.ps1
# Description:
#   Generates a CA + server certificate pair for a local HTTPS webhook
#   and produces a ValidatingWebhookConfiguration YAML with embedded CA.
#
# Requirements:
#   - OpenSSL must be installed and available in PATH
#   - PowerShell 5+ (works on Windows 10+)
# ===============================

# --- Configurable values ---
$CertDir = "certs"
$YamlFile = "validating-webhook.yaml"
$DaysValid = 365
$CommonName = "host.minikube.internal"
$SANs = "DNS:host.minikube.internal,IP:127.0.0.1"
$WebhookName = "local-validator"
$WebhookURL = "https://host.minikube.internal:8443/validate"
$WebhookPath = "/validate"
$TargetResources = "pods"

# --- Setup directory ---
Write-Host "📂 Creating directory: $CertDir"
New-Item -ItemType Directory -Force -Path $CertDir | Out-Null

# --- 1️⃣ Generate CA key and certificate ---
Write-Host "🔐 Generating self-signed CA certificate..."
openssl req -x509 -newkey rsa:4096 -days $DaysValid `
  -keyout "$CertDir\ca.key" -out "$CertDir\ca.crt" `
  -subj "/CN=Local Dev CA" -nodes | Out-Null

# --- 2️⃣ Generate server key and CSR ---
Write-Host "🧾 Generating server key and CSR..."
openssl req -newkey rsa:2048 -nodes `
  -keyout "$CertDir\server.key" -out "$CertDir\server.csr" `
  -subj "/CN=$CommonName" | Out-Null

# --- 3️⃣ Create extfile with SANs ---
Write-Host "🧩 Adding Subject Alternative Names (SANs)..."
@"
subjectAltName=$SANs
extendedKeyUsage=serverAuth
"@ | Out-File "$CertDir\extfile.cnf" -Encoding ascii

# --- 4️⃣ Sign server cert with CA ---
Write-Host "🖋️ Signing server certificate with CA..."
openssl x509 -req -in "$CertDir\server.csr" `
  -CA "$CertDir\ca.crt" -CAkey "$CertDir\ca.key" -CAcreateserial `
  -out "$CertDir\server.crt" -days $DaysValid -extfile "$CertDir\extfile.cnf" | Out-Null

# --- 5️⃣ Generate Base64-encoded CA for caBundle ---
$CaBundle = [Convert]::ToBase64String([IO.File]::ReadAllBytes("$CertDir\ca.crt"))

# --- 6️⃣ Generate ValidatingWebhookConfiguration YAML ---
Write-Host "🧾 Creating Kubernetes ValidatingWebhookConfiguration..."
@"
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: $WebhookName
webhooks:
  - name: ${WebhookName}.example.com
    admissionReviewVersions: ["v1"]
    sideEffects: None
    failurePolicy: Fail
    clientConfig:
      url: "$WebhookURL"
      caBundle: "$CaBundle"
    rules:
      - apiGroups: [""]
        apiVersions: ["v1"]
        operations: ["CREATE", "UPDATE"]
        resources: ["$TargetResources"]
"@ | Out-File $YamlFile -Encoding utf8

# --- 7️⃣ Summary ---
Write-Host "`n✅ Certificates and Webhook YAML generated successfully!"
Write-Host "📂 Certificates in: $CertDir"
Write-Host "📜 YAML file: $YamlFile"
Write-Host "`n🔗 Webhook URL: $WebhookURL"
Write-Host "⚙️  Apply YAML to Minikube with:"
Write-Host "   kubectl apply -f $YamlFile"
Write-Host "`n📋 To verify from Minikube:"
Write-Host "   minikube ssh"
Write-Host "   curl -vk --cacert /path/to/ca.crt $WebhookURL/healthz"
