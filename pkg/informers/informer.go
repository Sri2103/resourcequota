package informers

import (
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

func NewNamespaceInformer(clientset kubernetes.Interface) informers.SharedInformerFactory {
	factory := informers.NewSharedInformerFactory(clientset, 30*time.Second)
	return factory
}
