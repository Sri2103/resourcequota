package webhook

import (
	"fmt"
	"log"
	"sync"
	"time"

	platformv1alpha1 "github.com/sri2103/resource-quota-enforcer/pkg/apis/platform/v1alpha1"
	clientset "github.com/sri2103/resource-quota-enforcer/pkg/generated/clientset/versioned"
	informers "github.com/sri2103/resource-quota-enforcer/pkg/generated/informers/externalversions"
	listers "github.com/sri2103/resource-quota-enforcer/pkg/generated/listers/platform/v1alpha1"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// PolicyCacheIF defines interface for webhook cache operations.
type PolicyCacheIF interface {
	Get(namespace string) (*platformv1alpha1.ResourceQuotaPolicySpec, bool)
	Invalidate(namespace string)
	Run(stopCh <-chan struct{})
	WaitForReady(timeout time.Duration) error
}

// TypedPolicyCache uses generated informers + listers for fast CRD lookups.
type TypedPolicyCache struct {
	client   clientset.Interface
	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer
	lister   listers.ResourceQuotaPolicyLister

	readyMtx sync.RWMutex
	ready    bool
}

// NewTypedPolicyCache creates a new informer-backed cache.
func NewTypedPolicyCache(client clientset.Interface, resync time.Duration) *TypedPolicyCache {
	factory := informers.NewSharedInformerFactory(client, resync)
	inf := factory.Platform().V1alpha1().ResourceQuotaPolicies().Informer()
	lister := factory.Platform().V1alpha1().ResourceQuotaPolicies().Lister()

	return &TypedPolicyCache{
		client:   client,
		factory:  factory,
		informer: inf,
		lister:   lister,
	}
}

// Run starts the informer factory and marks cache as ready after sync.
func (pc *TypedPolicyCache) Run(stopCh <-chan struct{}) {
	log.Println("[Cache] Starting informer factory...")
	pc.factory.Start(stopCh)

	if ok := cache.WaitForCacheSync(stopCh, pc.informer.HasSynced); !ok {
		log.Println("[Cache] ❌ Cache sync failed")
		return
	}

	pc.readyMtx.Lock()
	pc.ready = true
	pc.readyMtx.Unlock()
	log.Println("[Cache] ✅ Cache synced successfully")

	<-stopCh
}

// Get retrieves policy spec for a namespace.
func (pc *TypedPolicyCache) Get(namespace string) (*platformv1alpha1.ResourceQuotaPolicySpec, bool) {
	pc.readyMtx.RLock()
	if !pc.ready {
		pc.readyMtx.RUnlock()
		return nil, false
	}
	pc.readyMtx.RUnlock()

	nsLister := pc.lister.ResourceQuotaPolicies(namespace)
	if nsLister == nil {
		// Namespace hasn’t been indexed yet
		return nil, false
	}

	policies, err := nsLister.List(labels.Everything())
	if err != nil || len(policies) == 0 {
		return nil, false
	}

	return &policies[0].Spec, true
}

// Invalidate is a no-op (informers keep the cache up-to-date automatically).
func (pc *TypedPolicyCache) Invalidate(namespace string) {}

// WaitForReady waits until informer cache is synced or times out.
func (pc *TypedPolicyCache) WaitForReady(timeout time.Duration) error {
	t := time.After(timeout)
	tick := time.Tick(200 * time.Millisecond)
	for {
		select {
		case <-t:
			return fmt.Errorf("timeout waiting for cache ready")
		case <-tick:
			pc.readyMtx.RLock()
			r := pc.ready
			pc.readyMtx.RUnlock()
			if r {
				return nil
			}
		}
	}
}
