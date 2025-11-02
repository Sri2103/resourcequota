package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	ReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "resource_quota_enforcer_reconcile_total",
			Help: "Number of reconcile attempts per resource",
		},
		[]string{"resource", "namespace"},
	)

	ReconcileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "resource_quota_enforcer_reconcile_errors_total",
			Help: "Number of reconcile errors per resource",
		},
		[]string{"resource", "namespace"},
	)

	EnforcementActions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "resource_quota_enforcer_actions_total",
			Help: "Number of enforcement actions taken by policy",
		},
		[]string{"action", "namespace"},
	)
)

func InitMetrics() {
	prometheus.MustRegister(ReconcileTotal, ReconcileErrors, EnforcementActions)
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe(":2112", nil)
	}()
}
