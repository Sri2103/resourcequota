package controller

import (
	"log"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type Controller struct {
	clientset *kubernetes.Clientset
	informer  cache.SharedIndexInformer
}

func NewController(clientset *kubernetes.Clientset, informer cache.SharedIndexInformer) *Controller {
	return &Controller{
		clientset: clientset,
		informer:  informer,
	}
}

func (c *Controller) Run(stopCh <-chan struct{}) {
	c.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ns := obj.(*corev1.Namespace)
			log.Printf("[ADD] Namespace: %s\n", ns.Name)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			ns := newObj.(*corev1.Namespace)
			log.Printf("[UPDATE] Namespace: %s\n", ns.Name)
		},
		DeleteFunc: func(obj interface{}) {
			ns := obj.(*corev1.Namespace)
			log.Printf("[DELETE] Namespace: %s\n", ns.Name)
		},
	})
	c.informer.Run(stopCh)
}
