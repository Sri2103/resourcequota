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

	"github.com/sri2103/resource-quota-enforcer/pkg/client"
	clientset "github.com/sri2103/resource-quota-enforcer/pkg/generated/clientset/versioned"
	"github.com/sri2103/resource-quota-enforcer/pkg/webhook"
)

func main() {
	var tlsCertFile string
	var tlsKeyFile string
	var listenAddr string
	var resync time.Duration

	flag.StringVar(&tlsCertFile, "tls-cert-file", "./certs/server.crt", "Path to TLS certificate")
	flag.StringVar(&tlsKeyFile, "tls-key-file", "./certs/server.key", "Path to TLS private key")
	flag.StringVar(&listenAddr, "listen", ":8443", "Webhook server listen address")
	flag.DurationVar(&resync, "resync", 30*time.Second, "Informer resync period")
	flag.Parse()

	cfg, err := client.PrepareConfig()
	if err != nil {
		log.Fatalf("[Main] ❌ Failed to build kubeconfig: %v", err)
	}

	cs, err := client.GetKubernetesClient(cfg)
	if err != nil {
		log.Fatalf("[Main] ❌ Failed to create core clientset: %v", err)
	}

	typedClient, err := clientset.NewForConfig(cfg)
	if err != nil {
		log.Fatalf("[Main] ❌ Failed to create typed clientset: %v", err)
	}

	webhook.InitMetrics()

	// Create informer-based cache
	policyCache := webhook.NewTypedPolicyCache(typedClient, resync)

	// Start informer factory
	stopCh := make(chan struct{})
	go policyCache.Run(stopCh)

	// Wait for cache sync
	if err := policyCache.WaitForReady(30 * time.Second); err != nil {
		log.Printf("[Main] ⚠️ Policy cache not ready in time: %v (continuing; cache misses possible)", err)
	} else {
		log.Println("[Main] ✅ Policy cache ready")
	}

	// Create webhook server
	server := webhook.NewWebhookServerWithInformer(cs, policyCache)

	// Routes
	mux := http.NewServeMux()
	mux.HandleFunc("/validate", server.HandleValidatePods)
	mux.HandleFunc("/mutate", server.InvalidateHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/metrics", webhook.MetricsHandler())

	// TLS setup
	cert, err := tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
	if err != nil {
		log.Fatalf("[Main] ❌ Failed to load cert/key: %v", err)
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

	// Graceful shutdown
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Printf("[Main] 🚀 Starting webhook server on %s", listenAddr)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Main] ❌ Webhook server failed: %v", err)
		}
	}()

	<-sigCh
	log.Println("[Main] 📴 Shutting down webhook server")
	close(stopCh)
	_ = srv.Close()
}
