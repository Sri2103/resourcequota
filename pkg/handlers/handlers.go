package handlers

import (
	"context"
	"log"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Policy struct {
	MaxPods   int
	MaxCPU    resource.Quantity
	MaxMemory resource.Quantity
}

type PodEnforcer struct {
	Client      *kubernetes.Clientset
	PolicyCache map[string]Policy // namespace → policy
}

func (e *PodEnforcer) Enforce(namespace string) {
	pods, err := e.Client.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Printf("❌ Error listing pods in %s: %v", namespace, err)
		return
	}

	policy, exists := e.PolicyCache[namespace]
	if !exists {
		return
	}

	// --- Enforce pod count ---
	if len(pods.Items) > policy.MaxPods {
		log.Printf("⚠️ Namespace %s exceeds Pod quota (%d/%d). Deleting oldest...", namespace, len(pods.Items), policy.MaxPods)
		e.deleteOldestPod(namespace, pods.Items)
		return
	}

	// --- Enforce CPU and Memory ---
	totalCPU := resource.MustParse("0")
	totalMem := resource.MustParse("0")

	for _, pod := range pods.Items {
		for _, c := range pod.Spec.Containers {
			if cpuReq, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
				totalCPU.Add(cpuReq)
			}
			if memReq, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
				totalMem.Add(memReq)
			}
		}
	}

	if totalCPU.Cmp(policy.MaxCPU) > 0 {
		log.Printf("⚠️ Namespace %s exceeds CPU quota (%s/%s). Deleting newest pod...", namespace, totalCPU.String(), policy.MaxCPU.String())
		e.deleteNewestPod(namespace, pods.Items)
	}

	if totalMem.Cmp(policy.MaxMemory) > 0 {
		log.Printf("⚠️ Namespace %s exceeds Memory quota (%s/%s). Deleting newest pod...", namespace, totalMem.String(), policy.MaxMemory.String())
		e.deleteNewestPod(namespace, pods.Items)
	}
}

func (e *PodEnforcer) deleteOldestPod(namespace string, pods []corev1.Pod) {
	sort.Slice(pods, func(i, j int) bool {
		return pods[i].CreationTimestamp.Before(&pods[j].CreationTimestamp)
	})
	target := pods[0]
	err := e.Client.CoreV1().Pods(namespace).Delete(context.TODO(), target.Name, metav1.DeleteOptions{})
	if err != nil {
		log.Printf("❌ Failed to delete pod %s/%s: %v", namespace, target.Name, err)
		return
	}
	log.Printf("🗑️ Deleted oldest pod %s/%s", namespace, target.Name)
}

func (e *PodEnforcer) deleteNewestPod(namespace string, pods []corev1.Pod) {
	sort.Slice(pods, func(i, j int) bool {
		return pods[i].CreationTimestamp.After(pods[j].CreationTimestamp.Time)
	})
	target := pods[0]
	err := e.Client.CoreV1().Pods(namespace).Delete(context.TODO(), target.Name, metav1.DeleteOptions{})
	if err != nil {
		log.Printf("❌ Failed to delete pod %s/%s: %v", namespace, target.Name, err)
		return
	}
	log.Printf("🗑️ Deleted newest pod %s/%s", namespace, target.Name)
}

func ParsePolicy(spec map[string]interface{}) Policy {
	maxPods := 10
	maxCPU := resource.MustParse("2")
	maxMem := resource.MustParse("2Gi")

	if v, ok := spec["maxPods"].(int64); ok {
		maxPods = int(v)
	}
	if v, ok := spec["maxCPU"].(string); ok {
		q, err := resource.ParseQuantity(v)
		if err == nil {
			maxCPU = q
		}
	}
	if v, ok := spec["maxMemory"].(string); ok {
		q, err := resource.ParseQuantity(v)
		if err == nil {
			maxMem = q
		}
	}

	log.Printf("📋 Parsed policy: Pods=%d CPU=%s Mem=%s", maxPods, maxCPU.String(), maxMem.String())
	return Policy{MaxPods: maxPods, MaxCPU: maxCPU, MaxMemory: maxMem}
}
