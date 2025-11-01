package webhook

import (
	"context"
	"testing"
	"time"

	"github.com/sri2103/resource-quota-enforcer/pkg/apis/platform/v1alpha1"
	fake "github.com/sri2103/resource-quota-enforcer/pkg/generated/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestInformerPolicyCache_GetAndReady(t *testing.T) {
	scheme := runtime.NewScheme()
	// Use fake dynamic client
	fake.AddToScheme(scheme)
	// gen := fake.NewSimpleDynamicClient(scheme)
	gen := fake.NewSimpleClientset()

	cache := NewTypedPolicyCache(gen, 10*time.Second)
	stopCh := make(chan struct{})
	defer close(stopCh)
	go cache.Run(stopCh)

	// wait for readiness (timeout short since fake client is immediate)
	if err := cache.WaitForReady(2 * time.Second); err != nil {
		t.Fatalf("cache not ready: %v", err)
	}


	obj := v1alpha1.ResourceQuotaPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ps1",
			Namespace: "ns1",
		},
		Spec: v1alpha1.ResourceQuotaPolicySpec{
			MaxPods:   2,
			MaxCPU:    "500m",
			MaxMemory: "256Mi",
		},
	}

	_, err := gen.PlatformV1alpha1().ResourceQuotaPolicies("ns1").Create(context.TODO(), &obj, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create policy failed: %v", err)
	}

	// wait briefly for informer to pick up
	time.Sleep(200 * time.Millisecond)

	spec, found := cache.Get("ns1")
	if !found {
		t.Fatalf("expected policy found in cache")
	}
	if spec == nil || spec.MaxPods == 0 {
		t.Fatalf("spec missing maxPods")
	}
}
