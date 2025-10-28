package webhook

import (
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dyninformers "k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// PolicyCacheIF defines the methods the controller/webhook will use.
type PolicyCacheIF interface {
	Get(namespace string) (map[string]interface{}, bool)
	Invalidate(namespace string)
	Run(stopCh <-chan struct{})
}

// InformerPolicyCache implements PolicyCacheIF using a dynamic informer + lister.
type InformerPolicyCache struct {
	gvr       schema.GroupVersionResource
	dynClient dynamic.Interface
	factory   dyninformers.DynamicSharedInformerFactory
	informer  cache.SharedIndexInformer
	store     cache.Indexer
	mtx       sync.RWMutex
	ready     bool
	readyMtx  sync.RWMutex
}

// NewInformerPolicyCache constructs the informer-backed cache.
// resync is the resync period for the informer factory.
func NewInformerPolicyCache(dyn dynamic.Interface, gvr schema.GroupVersionResource, resync time.Duration) *InformerPolicyCache {
	factory := dyninformers.NewDynamicSharedInformerFactory(dyn, resync)
	informer := factory.ForResource(gvr).Informer()
	return &InformerPolicyCache{
		gvr:       gvr,
		dynClient: dyn,
		factory:   factory,
		informer:  informer,
		store:     informer.GetIndexer(),
	}
}

// Run starts the informer factory and waits for cache sync.
func (pc *InformerPolicyCache) Run(stopCh <-chan struct{}) {
	pc.factory.Start(stopCh)
	if ok := cache.WaitForCacheSync(stopCh, pc.informer.HasSynced); !ok {
		// if sync failed, we'll leave ready as false
		return
	}
	pc.readyMtx.Lock()
	pc.ready = true
	pc.readyMtx.Unlock()
	<-stopCh
}

// Get returns the spec map for a namespace. If multiple policies exist, pick the first.
// Returns (spec, found)
func (pc *InformerPolicyCache) Get(namespace string) (map[string]interface{}, bool) {
	pc.readyMtx.RLock()
	if !pc.ready {
		pc.readyMtx.RUnlock()
		return nil, false
	}
	pc.readyMtx.RUnlock()

	// list keys in store filtered by namespace
	// the store contains unstructured.Unstructured objects
	objects := pc.store.List()
	for _, obj := range objects {
		if u, ok := obj.(*unstructured.Unstructured); ok {
			if u.GetNamespace() != namespace {
				continue
			}
			if spec, found, _ := unstructured.NestedMap(u.Object, "spec"); found {
				return spec, true
			}
			// if object found but no spec, treat as not found
		}
	}
	return nil, false
}

// Invalidate is a no-op for informer cache but provided to satisfy interface.
// Optionally we could remove items from store, but informer will refresh on CR update.
func (pc *InformerPolicyCache) Invalidate(namespace string) {
	// no-op since informer updates store on CR changes
	// we keep a hook in case you want to implement manual eviction
}

// Helper: returns if cache is ready
func (pc *InformerPolicyCache) Ready() bool {
	pc.readyMtx.RLock()
	defer pc.readyMtx.RUnlock()
	return pc.ready
}

// Utility: await readiness with timeout (useful in tests)
func (pc *InformerPolicyCache) WaitForReady(timeout time.Duration) error {
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
