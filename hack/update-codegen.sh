#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
MODULE=github.com/sri2103/resource-quota-enforcer
APIS_PKG=${MODULE}/pkg/apis
OUTPUT_PKG=${MODULE}/pkg/generated
OUTPUT_DIR=${SCRIPT_ROOT}/pkg/generated

echo "${SCRIPT_ROOT}"
echo "${BASH_SOURCE}"
echo "$(cd "${SCRIPT_ROOT}" && pwd)"


# locate code-generator package
if CODEGEN_PKG=$(go list -m -f '{{.Dir}}' k8s.io/code-generator 2>/dev/null); then
  echo "Using code-generator from module cache: ${CODEGEN_PKG}"
else
  CODEGEN_PKG="${GOPATH}/pkg/mod/k8s.io/code-generator@$(go list -m -f '{{.Version}}' k8s.io/code-generator 2>/dev/null || echo 'v0.31.0')"
  echo "Falling back to GOPATH: ${CODEGEN_PKG}"
fi

# source the kube_codegen script
source "${CODEGEN_PKG}/kube_codegen.sh"

# run helpers & client generation
# kube::codegen::gen_helpers \
#   --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
#   .

echo "${OUTPUT_DIR}"

kube::codegen::gen_client \
  --output-pkg "${OUTPUT_PKG}" \
  --output-dir "${OUTPUT_DIR}" \
  --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt"\
  --with-watch \
   "${SCRIPT_ROOT}/pkg/apis/"
  
echo "✅ Code generation complete"


