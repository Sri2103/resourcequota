package controller

import (
	"context"
	"log"
	"time"

	"github.com/sri2103/resource-quota-enforcer/pkg/handlers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type Controller struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	podInformer   cache.SharedIndexInformer
	nsInformer    cache.SharedIndexInformer
	enforcer      *handlers.PodEnforcer
}

func NewController(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, podInformer, nsInformer cache.SharedIndexInformer, enforcer *handlers.PodEnforcer) *Controller {
	return &Controller{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		podInformer:   podInformer,
		nsInformer:    nsInformer,
		enforcer:      enforcer,
	}
}

func (c *Controller) Run(stopCh <-chan struct{}) {
	c.nsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ns := obj.(*corev1.Namespace)
			log.Printf("[NS ADD] %s", ns.Name)
			c.syncPolicy(ns.Name)
		},
		UpdateFunc: func(_, newObj interface{}) {
			ns := newObj.(*corev1.Namespace)
			c.syncPolicy(ns.Name)
		},
	})

	c.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			c.enforcer.Enforce(pod.Namespace)
		},
		UpdateFunc: func(_, newObj interface{}) {
			pod := newObj.(*corev1.Pod)
			c.enforcer.Enforce(pod.Namespace)
		},
		DeleteFunc: func(obj interface{}) {
			pod := obj.(*corev1.Pod)
			c.enforcer.Enforce(pod.Namespace)
		},
	})

	go c.nsInformer.Run(stopCh)
	go c.podInformer.Run(stopCh)

	// Periodic recheck every 60s
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.fullSync()
			case <-stopCh:
				return
			}
		}
	}()

	<-stopCh
}

func (c *Controller) syncPolicy(namespace string) {
	gvr := schema.GroupVersionResource{
		Group:    "platform.example.com",
		Version:  "v1alpha1",
		Resource: "resourcequotapolicies",
	}

	list, err := c.dynamicClient.Resource(gvr).Namespace(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Printf("❌ Failed to list policies for %s: %v", namespace, err)
		return
	}

	for _, item := range list.Items {
		spec, found, _ := unstructured.NestedMap(item.Object, "spec")
		if !found {
			continue
		}
		policy := handlers.ParsePolicy(spec)
		c.enforcer.PolicyCache[namespace] = policy
		log.Printf("✅ Loaded policy for %s: %+v", namespace, policy)
	}
}

func (c *Controller) fullSync() {
	namespaces, err := c.clientset.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Printf("❌ Error listing namespaces: %v", err)
		return
	}

	for _, ns := range namespaces.Items {
		log.Printf("🔄 Syncing %s...", ns.Name)
		c.syncPolicy(ns.Name)
		c.enforcer.Enforce(ns.Name)
	}
}
