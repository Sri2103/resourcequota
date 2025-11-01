# 🛡️ Resource Quota Enforcer

**Resource Quota Enforcer** is a Kubernetes operator built using the **client-go** library.
It automatically enforces **per-namespace resource usage limits** (CPU, memory, pods, and services) defined via a custom resource called **`ResourceQuotaPolicy`**.

Unlike static Kubernetes ResourceQuotas, this controller continuously watches namespace activity, applies policies dynamically, and maintains compliance through a reconciliation loop.

---

## 📋 Table of Contents

- [Overview](#overview)
- [Features](#features)
- [Architecture](#architecture)
- [Project Structure](#project-structure)
- [CRD Specification](#crd-specification)
- [Controller Workflow](#controller-workflow)
- [Health \& Metrics](#health--metrics)
- [Local Development](#local-development)
- [Deployment](#deployment)
- [Prometheus Metrics](#prometheus-metrics)
- [Contributing](#contributing)
- [License](#license)

---

## 🌐 Overview

`ResourceQuotaEnforcer` continuously monitors namespaces and ensures that workloads stay within the limits defined in the `ResourceQuotaPolicy` CRD.

If a namespace exceeds allowed CPU, memory, or object count (pods/services), the controller automatically enforces limits by **rejecting new resources** and **logging violations**.

This controller is designed to work in **any Kubernetes cluster**, including **Minikube**, and uses **typed clients generated via `client-go`**.

---

## 🚀 Features

- 🔍 **Custom Resource Definition:** Define namespace-level quota policies dynamically.
- ⚙️ **Reactive Enforcement:** Watches pods, services, and custom resources for live updates.
- 🔁 **Periodic Sync:** Performs periodic rechecks to catch missed or stale states.
- 🧠 **Workqueue \& Backoff:** Uses rate-limited queues with exponential backoff for reliability.
- ❤️ **Health Endpoints:** Exposes `/healthz` and `/readyz` endpoints for Kubernetes probes.
- 📈 **Prometheus Metrics:** Exports key metrics for monitoring enforcement activity.
- 🧩 **Typed Clients:** Uses generated clients, informers, and listers for type safety.
- 🧹 **Graceful Shutdown:** Handles `SIGINT` and `SIGTERM` for clean exits.
- ☁️ **Cloud-Agnostic:** No dependencies on specific cloud provider APIs.

---

## 🧱 Architecture

```
                    +------------------------+
                    |  ResourceQuotaPolicy CR |
                    +-----------+------------+
                                |
                                | Watch via CR Informer
                                v
      +---------------------------------------+
      |          ResourceQuota Enforcer       |
      |---------------------------------------|
      |  - Typed Clientset                     |
      |  - Informers (CR, Pod, Svc)           |
      |  - Listers & Workqueue                 |
      |  - Controller Workers                  |
      |  - Sync Logic & Enforcement            |
      +---------------------------------------+
                  |             ^
                  |             |
     +------------+             +----------------+
     |                                          |
     v                                          v
 Pod Watcher                           Service Watcher
(Enforce CPU/Memory)                (Enforce Count Limits)
```

---

## Project Structure

```
resource-quota-enforcer/
├── cmd/
│   └── main.go                 # Entry point: initializes clients, controllers, signals
├── pkg/
│   ├── api/                   # CRD types (ResourceQuotaPolicy)
│   ├── client/                # Generated clientsets, informers, listers
│   ├── controller/            # Core reconciliation logic
│   ├── health/                # Health probes (readiness/liveness)
│   └── metrics/               # Prometheus exporter & metrics registry
├── deploy/
│   ├── crd.yaml               # CustomResourceDefinition manifest
│   ├── rbac.yaml              # Roles and permissions
│   └── deployment.yaml        # Controller Deployment manifest
├── hack/
│   └── boilerplate.go.txt     # License header for code-gen
└── go.mod / go.sum            # Module dependencies
```

---

## ⚙️ Controller Workflow

1. **Watch Events:**
   Informers for ResourceQuotaPolicy, Pods, and Services watch API changes.
2. **Queue Work Items:**
   Events trigger a rate-limited queue with namespace keys.
3. **Sync Loop:**
   The controller periodically reconciles namespace resource usage vs defined quotas.

**Enforcement:**
If usage exceeds the quota, future pod/service creations are rejected.
Violations are logged and exposed via Prometheus metrics.

**Backoff:**
Failed reconciliations are requeued with exponential backoff.

---

## Health \& Metrics

- `/healthz` → Reports controller health (always OK if running).
- `/readyz` → Reports readiness (only true when informers are synced).

### Prometheus Exporter

- `/metrics` → Exposes custom metrics:
  - `resource_enforcer_enforced_total`
  - `resource_enforcer_violation_total`
  - `resource_enforcer_sync_duration_seconds`
  - `resource_enforcer_errors_total`

---

## Local Development \& Deployment

1. Start Minikube:

```bash
minikube start
```

2. Apply CRD:

```bash
kubectl apply -f deploy/crd.yaml
```

3. Deploy controller:

```bash
kubectl apply -f deploy/rbac.yaml
kubectl apply -f deploy/deployment.yaml
```

4. Create a ResourceQuotaPolicy:

```bash
kubectl apply -f examples/policy.yaml
```

5. Check controller logs:

```bash
kubectl logs -n kube-system deploy/resource-quota-enforcer
```

---

## 📊 Prometheus Metrics

Metrics endpoint runs on port `:8080` by default:

```bash
kubectl port-forward svc/resource-quota-enforcer 8080:8080
curl localhost:8080/metrics
```

### Prometheus scrape config example

```yaml
- job_name: "resource-quota-enforcer"
  static_configs:
    - targets: ["resource-quota-enforcer.default.svc.cluster.local:8080"]
```

---

## Contributing

(Section placeholder for contributing guidelines)

## License

(Section placeholder for license details)
