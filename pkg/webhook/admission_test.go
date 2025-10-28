package webhook

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclient "k8s.io/client-go/kubernetes/fake"
)

func TestEvaluatePodAgainstPolicy_PodsLimit(t *testing.T) {
	cs := fakeclient.NewSimpleClientset()
	// create 2 existing pods in ns test-ns
	ns := "test-ns"
	for i := 0; i < 2; i++ {
		_, _ = cs.CoreV1().Pods(ns).Create(context.TODO(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p" + string('0'+i), Namespace: ns},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "c", Image: "busybox"}},
			},
		}, metav1.CreateOptions{})
	}

	srv := &WebhookServer{Clientset: cs}
	// policy: maxPods = 2
	spec := map[string]interface{}{"maxPods": int64(2)}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "new-pod", Namespace: ns},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "c",
					Image: "busybox",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
					},
				},
			},
		},
	}
	allowed, reason, err := srv.evaluatePodAgainstPolicy(context.TODO(), pod, ns, spec)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if allowed {
		t.Fatalf("expected denied due to pods limit, got allowed")
	}
	if reason == "" {
		t.Fatalf("expected reason message")
	}
}
