package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/sri2103/resource-quota-enforcer/pkg/client"
	"github.com/sri2103/resource-quota-enforcer/pkg/controller"
	"github.com/sri2103/resource-quota-enforcer/pkg/informers"
)

func main() {
	clientset, err := client.GetKubernetesClient()
	if err != nil {
		log.Fatalf("Error building client: %v", err)
	}

	factory := informers.NewNamespaceInformer(clientset)
	namespaceInformer := factory.Core().V1().Namespaces().Informer()

	ctrl := controller.NewController(clientset, namespaceInformer)

	stopCh := make(chan struct{})
	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGINT, syscall.SIGTERM)
	go ctrl.Run(stopCh)

	log.Println("Resource Quota Enforcer controller started 🚀")
	<-sigterm
	close(stopCh)
}
