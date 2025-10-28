package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var (
	// metrics
	metricAdmissionRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rqe_admission_requests_total",
		Help: "Total number of admission requests received",
	}, []string{"namespace", "result"})

	metricAdmissionViolations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "rqe_admission_violations_total",
		Help: "Total number of admission rejections by reason",
	}, []string{"namespace", "reason"})

	metricCacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "rqe_policy_cache_hits_total",
		Help: "Policy cache hits",
	})
	metricCacheMisses = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "rqe_policy_cache_misses_total",
		Help: "Policy cache misses",
	})
)

func init() {
	prometheus.MustRegister(metricAdmissionRequests, metricAdmissionViolations, metricCacheHits, metricCacheMisses)
}

type WebhookServer struct {
	DynClient dynamic.Interface
	Clientset kubernetes.Interface
	Decoder   runtime.Decoder
	Cache     PolicyCacheIF
	GVR       schema.GroupVersionResource
}

// NewWebhookServerWithInformer creates a server wired to a PolicyCacheIF (informer-based).
func NewWebhookServerWithInformer(dyn dynamic.Interface, cs *kubernetes.Clientset, cache PolicyCacheIF, gvr schema.GroupVersionResource) *WebhookServer {
	scheme := serializer.NewCodecFactory(nil).UniversalDeserializer()
	return &WebhookServer{
		DynClient: dyn,
		Clientset: cs,
		Decoder:   scheme,
		Cache:     cache,
		GVR:       gvr,
	}
}

// HandleValidatePods handles AdmissionReview v1 for Pod CREATE operations.
func (s *WebhookServer) HandleValidatePods(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var admissionReview admissionv1.AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&admissionReview); err != nil {
		http.Error(w, "could not decode admission review", http.StatusBadRequest)
		return
	}
	req := admissionReview.Request
	if req == nil {
		http.Error(w, "no admission request", http.StatusBadRequest)
		return
	}
	ns := req.Namespace
	metricAdmissionRequests.WithLabelValues(ns, "received").Inc()

	// allow if not Pod CREATE
	if req.Kind.Kind != "Pod" || req.Operation != admissionv1.Create {
		admissionReview.Response = &admissionv1.AdmissionResponse{Allowed: true, UID: req.UID}
		writeAdmissionResponse(w, &admissionReview)
		return
	}

	// decode Pod
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		admissionReview.Response = &admissionv1.AdmissionResponse{Allowed: true, UID: req.UID}
		writeAdmissionResponse(w, &admissionReview)
		return
	}

	// Get policy from informer cache
	spec, found := s.Cache.Get(ns)
	if found {
		metricCacheHits.Inc()
	} else {
		metricCacheMisses.Inc()
	}

	if !found || spec == nil {
		metricAdmissionRequests.WithLabelValues(ns, "allowed_no_policy").Inc()
		admissionReview.Response = &admissionv1.AdmissionResponse{Allowed: true, UID: req.UID}
		writeAdmissionResponse(w, &admissionReview)
		return
	}

	allowed, reason, err := s.evaluatePodAgainstPolicy(ctx, &pod, ns, spec)
	if err != nil {
		// fail-open on error
		admissionReview.Response = &admissionv1.AdmissionResponse{Allowed: true, UID: req.UID}
		writeAdmissionResponse(w, &admissionReview)
		return
	}
	if !allowed {
		metricAdmissionViolations.WithLabelValues(ns, reason).Inc()
		metricAdmissionRequests.WithLabelValues(ns, "denied").Inc()
		admissionReview.Response = &admissionv1.AdmissionResponse{
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("Pod denied by QuotaPolicy: %s", reason),
			},
			UID: req.UID,
		}
	} else {
		metricAdmissionRequests.WithLabelValues(ns, "allowed").Inc()
		admissionReview.Response = &admissionv1.AdmissionResponse{Allowed: true, UID: req.UID}
	}
	writeAdmissionResponse(w, &admissionReview)
}

// InvalidateHandler allows external caller to invalidate cache for a namespace.
// POST /invalidate with body: {"namespace":"ns1"}
func (s *WebhookServer) InvalidateHandler(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if payload.Namespace == "" {
		http.Error(w, "namespace required", http.StatusBadRequest)
		return
	}
	s.Cache.Invalidate(payload.Namespace)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"invalidated"}`))
}

// evaluatePodAgainstPolicy computes current usage and includes the new pod's requests.
// returns allowed(bool), reason(string), error
func (s *WebhookServer) evaluatePodAgainstPolicy(ctx context.Context, pod *corev1.Pod, namespace string, spec map[string]interface{}) (bool, string, error) {
	// parse policy fields similar to ParsePolicy
	maxPods := 0
	if v, ok := spec["maxPods"]; ok {
		switch t := v.(type) {
		case int64:
			maxPods = int(t)
		case float64:
			maxPods = int(t)
		case string:
			if n, err := strconv.Atoi(t); err == nil {
				maxPods = n
			}
		case int:
			maxPods = t
		}
	}
	maxCPU := resource.MustParse("0")
	if v, ok := spec["maxCPU"].(string); ok && v != "" {
		if q, err := resource.ParseQuantity(v); err == nil {
			maxCPU = q
		}
	}
	maxMem := resource.MustParse("0")
	if v, ok := spec["maxMemory"].(string); ok && v != "" {
		if q, err := resource.ParseQuantity(v); err == nil {
			maxMem = q
		}
	}

	// list existing pods using clientset
	pods, err := s.Clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return true, "", err
	}

	totalPods := 0
	totalCPU := resource.MustParse("0")
	totalMem := resource.MustParse("0")
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		totalPods++
		for _, c := range p.Spec.Containers {
			if q, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
				totalCPU.Add(q)
			}
			if q, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
				totalMem.Add(q)
			}
		}
	}

	// include new pod
	totalPods++
	for _, c := range pod.Spec.Containers {
		if q, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
			totalCPU.Add(q)
		}
		if q, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
			totalMem.Add(q)
		}
	}

	// checks
	if maxPods > 0 && totalPods > maxPods {
		return false, fmt.Sprintf("maxPods exceeded: %d > %d", totalPods, maxPods), nil
	}
	if maxCPU.Cmp(resource.MustParse("0")) > 0 && totalCPU.Cmp(maxCPU) > 0 {
		return false, fmt.Sprintf("cpu exceeded: %s > %s", totalCPU.String(), maxCPU.String()), nil
	}
	if maxMem.Cmp(resource.MustParse("0")) > 0 && totalMem.Cmp(maxMem) > 0 {
		return false, fmt.Sprintf("memory exceeded: %s > %s", totalMem.String(), maxMem.String()), nil
	}

	return true, "", nil
}

// writeAdmissionResponse helper
func writeAdmissionResponse(w http.ResponseWriter, review *admissionv1.AdmissionReview) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(review)
}

// MetricsHandler returns promhttp handler (for registration in main)
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}
