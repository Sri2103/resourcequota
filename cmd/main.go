package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/sri2103/resource-quota-enforcer/pkg/client"
	"github.com/sri2103/resource-quota-enforcer/pkg/controller"
	"github.com/sri2103/resource-quota-enforcer/pkg/handlers"
	"github.com/sri2103/resource-quota-enforcer/pkg/informers"
)

func main() {
	config, err := client.PrepareConfig()
	if err != nil {
		log.Fatalf("error loading config: %v", err)
	}
	clientset, err := client.GetKubernetesClient(config)
	if err != nil {
		log.Fatalf("Error building client: %v", err)
	}

	dynamicClient, err := client.DynamicClient(config)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
	}

	factory := informers.NewNamespaceInformer(clientset)
	podInformer := factory.Core().V1().Pods().Informer()
	nsInformer := factory.Core().V1().Namespaces().Informer()

	enforcer := &handlers.PodEnforcer{
		Client:      clientset,
		PolicyCache: make(map[string]handlers.Policy),
	}

	ctrl := controller.NewController(clientset, dynamicClient, podInformer, nsInformer, enforcer)

	stopCh := make(chan struct{})
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGINT, syscall.SIGTERM)
	go ctrl.Run(stopCh)

	log.Println("Resource Quota Enforcer controller started 🚀")
	<-sigterm
	close(stopCh)
}
