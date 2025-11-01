package controller

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/sri2103/resource-quota-enforcer/pkg/apis/platform/v1alpha1"
	"github.com/sri2103/resource-quota-enforcer/pkg/generated/clientset/versioned"
	"github.com/sri2103/resource-quota-enforcer/pkg/handlers"
	"github.com/sri2103/resource-quota-enforcer/pkg/health"
	metrics "github.com/sri2103/resource-quota-enforcer/pkg/metrics"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

type Controller struct {
	clientset kubernetes.Interface
	CRclient  versioned.Interface
	recorder  record.EventRecorder

	podInformer cache.SharedIndexInformer
	nsInformer  cache.SharedIndexInformer

	enforcer *handlers.PodEnforcer
	scheme   *runtime.Scheme

	queue     workqueue.TypedRateLimitingInterface[any]
	cacheLock sync.RWMutex
}

// NewController constructs the controller.
func NewController(clientset kubernetes.Interface, dynamicClient versioned.Interface, podInformer, nsInformer cache.SharedIndexInformer, enforcer *handlers.PodEnforcer, scheme *runtime.Scheme) *Controller {
	q := workqueue.
		NewNamedRateLimitingQueue(
			workqueue.DefaultTypedItemBasedRateLimiter[any](),
			"resource-quota-enforcer",
		)
	v1alpha1.Install(scheme)
	rec := record.NewBroadcaster()
	rec.StartRecordingToSink(&v1.EventSinkImpl{
		Interface: clientset.CoreV1().Events(""),
	})

	recorder := rec.NewRecorder(scheme, corev1.EventSource{Component: "resourcequotapolicy-controller"})

	return &Controller{
		clientset:   clientset,
		CRclient:    dynamicClient,
		podInformer: podInformer,
		nsInformer:  nsInformer,
		enforcer:    enforcer,
		queue:       q,
		recorder:    recorder,
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

	if ok := cache.WaitForCacheSync(stopCh, c.nsInformer.HasSynced, c.podInformer.HasSynced); !ok {
		log.Println("[Controller] ❌ Failed to sync caches, exiting...")
		return
	}

	health.SetReady()

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
	ctx := context.TODO()
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
		return c.syncHandler(ctx, ns)
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

// syncHandler ensures policy cache for namespace and runs enforcement.
// core reconciler logic
// It also updates CRD status (if policy CR exists).
func (c *Controller) syncHandler(ctx context.Context, ns string) error {
	klog.V(4).Infof("Reconciling namespace: %s", ns)

	// Step 1: List all CRs in this namespace
	list, err := c.CRclient.
		PlatformV1alpha1().
		ResourceQuotaPolicies(ns).
		List(ctx, metav1.ListOptions{})
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

		spec := item.Spec

		policy := handlers.ParsePolicy(&spec)

		// Update cache
		c.cacheLock.Lock()
		c.enforcer.PolicyCache[ns] = policy
		c.cacheLock.Unlock()

		// record event:
		c.recorder.Eventf(
			&item,
			corev1.EventTypeNormal,
			"ReconcileStarted",
			"Started reconciling ResourceQuotaPolicy %s", item.Name,
		)

		// Step 3: Enforce policy
		enforced, err := c.enforcer.EnforceUntilOK(ns, policy)
		metrics.ReconcileTotal.WithLabelValues("pod", ns).Inc()
		if err != nil {
			metrics.ReconcileErrors.WithLabelValues("pod", ns).Inc()
			klog.Errorf("enforce error for namespace %s: %v", ns, err)
			// 🔹 Record a failure event if enforcement failed
			c.recorder.Eventf(
				&item,
				corev1.EventTypeWarning,
				"EnforcementFailed",
				"Failed to enforce policy %s: %v", item.Name, err.Error(),
			)
			continue
		}

		// Step 4: Update status
		status := &v1alpha1.ResourceQuotaPolicyStatus{
			CurrentPods: enforced.CurrentPods,
			CPUUsage:    enforced.CurrentCPU,
			MemoryUsage: enforced.CurrentMemory,
			Violation:   enforced.Violation,
			Message:     enforced.Message,
		}

		if cr, err := c.updatePolicyStatus(ctx, ns, item.GetName(), status); err != nil {
			klog.Errorf("failed to update status for %s/%s: %v", ns, item.GetName(), err)
			continue
		} else {
			log.Printf("status of the updated: %v", cr.Status)
		}

		c.recorder.Eventf(
			&item,
			corev1.EventTypeNormal,
			"ReconcileSucceeded",
			"Successfully enforced ResourceQuotaPolicy %s", item.Name,
		)

	}

	klog.V(3).Infof("Finished syncing namespace %s", ns)
	return nil
}

// updatePolicyStatus writes the status subresource for CRD. If API server doesn't support subresource, fallback to Update.
func (c *Controller) updatePolicyStatus(ctx context.Context, namespace, name string, status *v1alpha1.ResourceQuotaPolicyStatus) (*v1alpha1.ResourceQuotaPolicy, error) {
	// get object
	obj, err := c.CRclient.
		PlatformV1alpha1().
		ResourceQuotaPolicies(namespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	obj.Status = *status

	// fallback to Update if UpdateStatus not allowed
	cr, err := c.CRclient.
		PlatformV1alpha1().
		ResourceQuotaPolicies(namespace).
		UpdateStatus(ctx, obj, metav1.UpdateOptions{})
	return cr, err
}
