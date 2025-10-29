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
	"k8s.io/klog/v2"
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
	log.Println("[Controller] Starting ResourceQuotaEnforcer controller...")

	defer func() {
		log.Println("[Controller] Shutting down work queue...")
		c.queue.ShutDown()
	}()

	// 1️⃣ Register event handlers
	c.nsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueueNamespace(obj) },
		UpdateFunc: func(_, newObj interface{}) { c.enqueueNamespace(newObj) },
		DeleteFunc: func(obj interface{}) { c.enqueueNamespace(obj) },
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

	// 2️⃣ Start informers
	go c.nsInformer.Run(stopCh)
	go c.podInformer.Run(stopCh)

	// 3️⃣ Wait for caches to sync before starting workers
	if ok := cache.WaitForCacheSync(stopCh, c.nsInformer.HasSynced, c.podInformer.HasSynced); !ok {
		log.Println("[Controller] ❌ Failed to sync caches, exiting...")
		return
	}

	// 4️⃣ Start worker goroutines
	log.Printf("[Controller] Starting %d workers...", workers)
	for i := 0; i < workers; i++ {
		go func(id int) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Worker-%d] ⚠️ Panic recovered: %v", id, r)
				}
			}()
			for c.processNextItem() {
			}
		}(i)
	}

	// 5️⃣ Periodic full resync
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				namespaces, err := c.clientset.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{})
				if err != nil {
					log.Printf("[Resync] Error listing namespaces: %v", err)
					continue
				}
				for _, ns := range namespaces.Items {
					c.queue.AddRateLimited(ns.Name)
				}
				log.Printf("[Resync] Queued %d namespaces for periodic enforcement", len(namespaces.Items))
			case <-stopCh:
				log.Println("[Resync] Stopping periodic sync loop")
				return
			}
		}
	}()

	// 6️⃣ Block until stop signal
	<-stopCh
	log.Println("[Controller] 🛑 Controller stopped gracefully")
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
	obj, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(obj)

	ns, ok := obj.(string)
	if !ok {
		klog.Errorf("expected string in workqueue but got %#v", obj)
		c.queue.Forget(obj)
		return true
	}

	err := func() (err error) {
		// Protect from unexpected panics inside syncNamespace
		defer func() {
			if r := recover(); r != nil {
				klog.Errorf("panic while syncing namespace %q: %v", ns, r)
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		return c.syncNamespace(ns)
	}()
	if err != nil {
		// Retry with rate limit
		c.queue.AddRateLimited(ns)
		klog.Errorf("error syncing namespace %q: %v (will retry)", ns, err)
		return true
	}

	// Successful reconciliation
	c.queue.Forget(ns)
	klog.Infof("successfully synced namespace %q", ns)
	return true
}

// syncNamespace ensures policy cache for namespace and runs enforcement.
// It also updates CRD status (if policy CR exists).
func (c *Controller) syncNamespace(ns string) error {
	klog.V(4).Infof("Reconciling namespace: %s", ns)

	// Step 1: List all CRs in this namespace
	list, err := c.dynamicClient.Resource(c.gvr).Namespace(ns).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("list CRs: %w", err)
	}

	if len(list.Items) == 0 {
		c.cacheLock.Lock()
		delete(c.enforcer.PolicyCache, ns)
		c.cacheLock.Unlock()
		klog.V(4).Infof("No policies found in namespace %s, removed from cache", ns)
		return nil
	}

	// Step 2: Process each CR (you can later extend for multiple)
	for _, item := range list.Items {
		spec, found, err := unstructured.NestedMap(item.Object, "spec")
		if err != nil {
			klog.Errorf("error reading spec for %s/%s: %v", ns, item.GetName(), err)
			continue
		}
		if !found {
			klog.Warningf("no spec found in %s/%s", ns, item.GetName())
			continue
		}

		policy := handlers.ParsePolicy(spec)

		// Update cache
		c.cacheLock.Lock()
		c.enforcer.PolicyCache[ns] = policy
		c.cacheLock.Unlock()

		// Step 3: Enforce policy
		enforced, err := c.enforcer.EnforceUntilOK(ns, policy)
		if err != nil {
			klog.Errorf("enforce error for namespace %s: %v", ns, err)
			continue
		}

		// Step 4: Update status
		status := v1alpha1.ResourceQuotaPolicyStatus{
			CurrentPods: enforced.CurrentPods,
			CPUUsage:    enforced.CurrentCPU,
			MemoryUsage: enforced.CurrentMemory,
			Violations:  []string{enforced.Message},
		}

		statusMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&status)
		if err != nil {
			klog.Errorf("failed to convert status for %s/%s: %v", ns, item.GetName(), err)
			continue
		}

		if err := c.updatePolicyStatus(ns, item.GetName(), statusMap); err != nil {
			klog.Errorf("failed to update status for %s/%s: %v", ns, item.GetName(), err)
			continue
		}
	}

	klog.V(3).Infof("Finished syncing namespace %s", ns)
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
