package webhook

import (
	"fmt"
	"sync"
	"time"

	platformv1alpha1 "github.com/sri2103/resource-quota-enforcer/pkg/apis/platform/v1alpha1"
	clientset "github.com/sri2103/resource-quota-enforcer/pkg/generated/clientset/versioned"
	informers "github.com/sri2103/resource-quota-enforcer/pkg/generated/informers/externalversions"
	listers "github.com/sri2103/resource-quota-enforcer/pkg/generated/listers/platform/v1alpha1"

	"k8s.io/client-go/tools/cache"
)

// PolicyCacheIF defines the methods used by the webhook.
type PolicyCacheIF interface {
	Get(namespace string) (*platformv1alpha1.ResourceQuotaPolicySpec, bool)
	Invalidate(namespace string)
	Run(stopCh <-chan struct{})
	WaitForReady(timeout time.Duration) error
}

// TypedPolicyCache uses generated informer + lister.
type TypedPolicyCache struct {
	client   clientset.Interface
	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer
	lister   listers.ResourceQuotaPolicyLister
	mtx      sync.RWMutex
	ready    bool
	readyMtx sync.RWMutex
}

// NewTypedPolicyCache constructs the informer-backed cache.
func NewTypedPolicyCache(client clientset.Interface, resync time.Duration) *TypedPolicyCache {
	factory := informers.NewSharedInformerFactory(client, resync)
	informer := factory.Platform().V1alpha1().ResourceQuotaPolicies().Informer()
	lister := factory.Platform().V1alpha1().ResourceQuotaPolicies().Lister()

	return &TypedPolicyCache{
		client:   client,
		factory:  factory,
		informer: informer,
		lister:   lister,
	}
}

// Run starts the informer factory and waits for cache sync.
func (pc *TypedPolicyCache) Run(stopCh <-chan struct{}) {
	pc.factory.Start(stopCh)
	if ok := cache.WaitForCacheSync(stopCh, pc.informer.HasSynced); !ok {
		return
	}
	pc.readyMtx.Lock()
	pc.ready = true
	pc.readyMtx.Unlock()
	<-stopCh
}

// Get retrieves the ResourceQuotaPolicy spec for a namespace.
func (pc *TypedPolicyCache) Get(namespace string) (*platformv1alpha1.ResourceQuotaPolicySpec, bool) {
	pc.readyMtx.RLock()
	if !pc.ready {
		pc.readyMtx.RUnlock()
		return nil, false
	}
	pc.readyMtx.RUnlock()

	// list all policies in the namespace
	policies, err := pc.lister.ResourceQuotaPolicies(namespace).List(nil)
	if err != nil || len(policies) == 0 {
		return nil, false
	}

	// return first policy's spec
	return &policies[0].Spec, true
}

// Invalidate — optional hook (no-op)
func (pc *TypedPolicyCache) Invalidate(namespace string) {}

// Ready helper
func (pc *TypedPolicyCache) Ready() bool {
	pc.readyMtx.RLock()
	defer pc.readyMtx.RUnlock()
	return pc.ready
}

// WaitForReady — waits until cache is synced or timeout occurs.
func (pc *TypedPolicyCache) WaitForReady(timeout time.Duration) error {
	t := time.After(timeout)
	tick := time.Tick(100 * time.Millisecond)
	for {
		select {
		case <-t:
			return fmt.Errorf("timeout waiting for cache ready")
		case <-tick:
			if pc.Ready() {
				return nil
			}
		}
	}
}
