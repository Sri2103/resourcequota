package controller

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sri2103/resource-quota-enforcer/pkg/apis/platform/v1alpha1"
	"github.com/sri2103/resource-quota-enforcer/pkg/handlers"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type Controller struct {
	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface

	podInformer cache.SharedIndexInformer
	nsInformer  cache.SharedIndexInformer

	enforcer *handlers.PodEnforcer
	scheme   *runtime.Scheme

	queue workqueue.RateLimitingInterface

	gvr       schema.GroupVersionResource
	cacheLock sync.RWMutex
}

// NewController constructs the controller.
func NewController(clientset *kubernetes.Clientset, dynamicClient dynamic.Interface, podInformer, nsInformer cache.SharedIndexInformer, enforcer *handlers.PodEnforcer, scheme *runtime.Scheme) *Controller {
	q := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "resource-quota-enforcer")
	v1alpha1.AddToScheme(scheme)

	return &Controller{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		podInformer:   podInformer,
		nsInformer:    nsInformer,
		enforcer:      enforcer,
		queue:         q,
		gvr: schema.GroupVersionResource{
			Group:    "platform.example.com",
			Version:  "v1alpha1",
			Resource: "resourcequotapolicies",
		},
	}
}

// Run starts informers and worker goroutines. `workers` is how many goroutines process the queue.
func (c *Controller) Run(stopCh <-chan struct{}, workers int) {
	defer c.queue.ShutDown()

	c.nsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) { c.enqueueNamespace(obj) },
		UpdateFunc: func(_, newObj interface{}) {
			c.enqueueNamespace(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			c.enqueueNamespace(obj)
		},
	})

	c.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if pod, ok := obj.(*corev1.Pod); ok {
				c.queue.AddRateLimited(pod.Namespace)
			}
		},
		UpdateFunc: func(_, newObj interface{}) {
			if pod, ok := newObj.(*corev1.Pod); ok {
				c.queue.AddRateLimited(pod.Namespace)
			}
		},
		DeleteFunc: func(obj interface{}) {
			if pod, ok := obj.(*corev1.Pod); ok {
				c.queue.AddRateLimited(pod.Namespace)
			}
		},
	})

	go c.nsInformer.Run(stopCh)
	go c.podInformer.Run(stopCh)

	// wait for caches to sync
	if ok := cache.WaitForCacheSync(stopCh, c.nsInformer.HasSynced, c.podInformer.HasSynced); !ok {
		log.Println("failed to wait for caches to sync")
		return
	}

	// start workers
	for i := 0; i < workers; i++ {
		go func() {
			for c.processNextItem() {
			}
		}()
	}

	// periodic full sync
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				namespaces, err := c.clientset.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
				if err != nil {
					log.Printf("fullSync list namespaces: %v", err)
					continue
				}
				for _, ns := range namespaces.Items {
					c.queue.AddRateLimited(ns.Name)
				}
			case <-stopCh:
				fmt.Println("error occured here")
				return
			}
		}
	}()

	<-stopCh
	log.Println("controller stopping")
}

func (c *Controller) enqueueNamespace(obj interface{}) {
	var nsName string
	switch t := obj.(type) {
	case *corev1.Namespace:
		nsName = t.Name
	case cache.DeletedFinalStateUnknown:
		if ns, ok := t.Obj.(*corev1.Namespace); ok {
			nsName = ns.Name
		}
	default:
		// ignore
		return
	}
	if nsName != "" {
		c.queue.Add(nsName)
	}
}

// processNextItem processes a single key from the queue.
func (c *Controller) processNextItem() bool {
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(key)

	nsName, ok := key.(string)
	if !ok {
		c.queue.Forget(key)
		return true
	}

	if err := c.syncNamespace(nsName); err != nil {
		// requeue with rate limiting on error
		c.queue.AddRateLimited(nsName)
		log.Printf("error syncing namespace %s: %v", nsName, err)
	} else {
		c.queue.Forget(nsName)
	}
	return true
}

// syncNamespace ensures policy cache for namespace and runs enforcement.
// It also updates CRD status (if policy CR exists).
func (c *Controller) syncNamespace(ns string) error {
	// List CRs in the namespace
	list, err := c.dynamicClient.Resource(c.gvr).Namespace(ns).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		// non-fatal; maybe CRD not installed or namespace has no policy
		return fmt.Errorf("list CRs: %w", err)
	}

	// If no policies exist, clear cache for this namespace
	if len(list.Items) == 0 {
		c.cacheLock.Lock()
		delete(c.enforcer.PolicyCache, ns)
		c.cacheLock.Unlock()
		return nil
	}

	// Use first policy (extend later for multiple)
	for _, item := range list.Items {
		spec, found, err := unstructured.NestedMap(item.Object, "spec")
		if err != nil {
			return fmt.Errorf("reading spec for %s/%s: %w", ns, item.GetName(), err)
		}
		if !found {
			continue
		}

		// Parse policy from spec
		p := handlers.ParsePolicy(spec)

		// Update in-memory cache safely
		c.cacheLock.Lock()
		c.enforcer.PolicyCache[ns] = p
		c.cacheLock.Unlock()

		// Run enforcement logic
		enforced, err := c.enforcer.EnforceUntilOK(ns, p)
		if err != nil {
			log.Printf("enforce error for ns %s: %v", ns, err)
			return err
		}

		// Prepare status update
		qstatus := v1alpha1.ResourceQuotaPolicyStatus{
			CurrentPods: enforced.CurrentPods,
			CPUUsage:    enforced.CurrentCPU,
			MemoryUsage: enforced.CurrentMemory,
			Violations:  []string{enforced.Message},
		}

		statusMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&qstatus)
		if err != nil {
			log.Printf("failed to convert status for %s/%s: %v", ns, item.GetName(), err)
			return err
		}

		// Update status in CR
		if updateErr := c.updatePolicyStatus(ns, item.GetName(), statusMap); updateErr != nil {
			log.Printf("failed to update status for %s/%s: %v", ns, item.GetName(), updateErr)
			return updateErr
		}

		// currently handle one CR per namespace (break if multiple)
		break
	}

	return nil
}

// updatePolicyStatus writes the status subresource for CRD. If API server doesn't support subresource, fallback to Update.
func (c *Controller) updatePolicyStatus(namespace, name string, status map[string]interface{}) error {
	// get object
	obj, err := c.dynamicClient.Resource(c.gvr).Namespace(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if err := unstructured.SetNestedField(obj.Object, status, "status"); err != nil {
		return err
	}

	_, err = c.dynamicClient.Resource(c.gvr).Namespace(namespace).UpdateStatus(context.TODO(), obj, metav1.UpdateOptions{})
	if err == nil {
		return nil
	}
	// fallback to Update if UpdateStatus not allowed
	_, err = c.dynamicClient.Resource(c.gvr).Namespace(namespace).Update(context.TODO(), obj, metav1.UpdateOptions{})
	return err
}
