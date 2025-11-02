package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sri2103/resource-quota-enforcer/pkg/client"
	"github.com/sri2103/resource-quota-enforcer/pkg/controller"
	platformv1alpha1 "github.com/sri2103/resource-quota-enforcer/pkg/generated/clientset/versioned"
	"github.com/sri2103/resource-quota-enforcer/pkg/handlers"
	"github.com/sri2103/resource-quota-enforcer/pkg/health"
	"github.com/sri2103/resource-quota-enforcer/pkg/informers"
	"github.com/sri2103/resource-quota-enforcer/pkg/metrics"
	"k8s.io/apimachinery/pkg/runtime"
)

func main() {
	// set up clients
	config, err := client.PrepareConfig()
	if err != nil {
		log.Fatalf("error loading config: %v", err)
	}
	clientset, err := client.GetKubernetesClient(config)
	if err != nil {
		log.Fatalf("Error building client: %v", err)
	}

	// custom resource client
	CRclient, err := platformv1alpha1.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
	}

	// factories and informers
	factory := informers.NewNamespaceInformer(clientset)
	podInformer := factory.Core().V1().Pods().Informer()
	nsInformer := factory.Core().V1().Namespaces().Informer()

	// enforcers to handle pod setups
	enforcer := &handlers.PodEnforcer{
		Client:      clientset,
		PolicyCache: make(map[string]handlers.Policy),
	}

	// start channels to block the main go routine
	stopCh := make(chan struct{})
	scheme := runtime.NewScheme()
	ctrl := controller.NewController(clientset, CRclient, podInformer, nsInformer, enforcer, scheme)

	// end signals
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGINT, syscall.SIGTERM)
	metrics.InitMetrics()

	// run the controller and
	go ctrl.Run(stopCh, 5)

	go startHealthAndMetrics()

	log.Println("Resource Quota Enforcer controller started 🚀")
	<-sigterm
	close(stopCh)
}

func startHealthAndMetrics() {
	mux := http.NewServeMux()

	// Health endpoints
	mux.HandleFunc("/healthz", health.HealthzHandler)
	mux.HandleFunc("/readyz", health.ReadyzHandler)

	// Prometheus metrics
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:         ":8080",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Println("📈 Metrics & health endpoints started on :8080")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Error running metrics server: %v", err)
	}
}

func StartMetrics() {
}
