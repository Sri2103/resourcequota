package webhook

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
)

func TestInformerPolicyCache_GetAndReady(t *testing.T) {
	scheme := runtime.NewScheme()
	// Use fake dynamic client
	gen := fake.NewSimpleDynamicClient(scheme)

	gvr := schema.GroupVersionResource{
		Group:    "platform.example.com",
		Version:  "v1alpha1",
		Resource: "resourcequotapolicies",
	}

	cache := NewInformerPolicyCache(gen, gvr, 10*time.Second)
	stopCh := make(chan struct{})
	defer close(stopCh)
	go cache.Run(stopCh)

	// wait for readiness (timeout short since fake client is immediate)
	if err := cache.WaitForReady(2 * time.Second); err != nil {
		t.Fatalf("cache not ready: %v", err)
	}

	// create a policy in fake client
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "platform.example.com/v1alpha1",
			"kind":       "ResourceQuotaPolicy",
			"metadata": map[string]interface{}{
				"name":      "p1",
				"namespace": "ns1",
			},
			"spec": map[string]interface{}{
				"maxPods":   int64(2),
				"maxCPU":    "500m",
				"maxMemory": "256Mi",
			},
		},
	}
	_, err := gen.Resource(gvr).Namespace("ns1").Create(context.TODO(), obj, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create policy failed: %v", err)
	}

	// wait briefly for informer to pick up
	time.Sleep(200 * time.Millisecond)

	spec, found := cache.Get("ns1")
	if !found {
		t.Fatalf("expected policy found in cache")
	}
	if _, ok := spec["maxPods"]; !ok {
		t.Fatalf("spec missing maxPods")
	}
}
