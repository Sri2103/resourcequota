#!/usr/bin/env bash
set -euo pipefail

export GO111MODULE=on
export GOPROXY=https://proxy.golang.org,direct

REPO=github.com/sri2103/resource-quota-enforcer
CODEGEN_PKG=$(go env GOPATH)/pkg/mod/k8s.io/code-generator@v0.31.0

bash "${CODEGEN_PKG}/kube_codegen.sh" \
  all \
  "${REPO}/pkg/generated" \
  "${REPO}/pkg/apis" \
   platform:v1alpha1 \
  --go-header-file hack/boilerplate.go.txt
