package main

import (
	"crypto/tls"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sri2103/resource-quota-enforcer/pkg/generated/clientset/versioned"
	"github.com/sri2103/resource-quota-enforcer/pkg/webhook"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	var kubeconfig string
	var tlsCertFile string
	var tlsKeyFile string
	var listenAddr string
	var resync time.Duration

	flag.StringVar(&kubeconfig, "kubeconfig", "", "kubeconfig path (optional)")
	flag.StringVar(&tlsCertFile, "tls-cert-file", "/tls/tls.crt", "tls cert")
	flag.StringVar(&tlsKeyFile, "tls-key-file", "/tls/tls.key", "tls key")
	flag.StringVar(&listenAddr, "listen", ":8443", "listen address")
	flag.DurationVar(&resync, "resync", 30*time.Second, "informer resync period")
	flag.Parse()

	// build kube config
	var cfg *rest.Config
	var err error
	if kubeconfig != "" {
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		cfg, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Fatalf("build config: %v", err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("kubernetes clientset: %v", err)
	}

	dyn, err := versioned.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("CR client: %v", err)
	}

	// GVR for ResourceQuotaPolicy (user confirmed)

	// informer-based cache
	policyCache := webhook.NewTypedPolicyCache(dyn, resync)

	// start informer factory in background
	stopCh := make(chan struct{})
	go policyCache.Run(stopCh)

	// wait for cache ready or timeout
	if err := policyCache.WaitForReady(10 * time.Second); err != nil {
		log.Printf("policy cache not ready in time: %v (continuing; cache misses possible)", err)
	}

	// create server
	server := webhook.NewWebhookServerWithInformer(dyn, cs, policyCache)

	// HTTP handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/validate-pods", server.HandleValidatePods)
	mux.HandleFunc("/invalidate", server.InvalidateHandler)
	mux.Handle("/metrics", webhook.MetricsHandler())

	cert, err := tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
	if err != nil {
		log.Fatalf("load cert/key: %v", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	srv := &http.Server{
		Addr:      listenAddr,
		Handler:   mux,
		TLSConfig: tlsCfg,
	}

	// graceful shutdown wiring
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("starting webhook server on %s", listenAddr)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("webhook server failed: %v", err)
		}
	}()

	<-sigCh
	log.Println("shutting down webhook server")
	close(stopCh)
	_ = srv.Close()
}
