package handlers

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Policy holds parsed values used for enforcement.
type Policy struct {
	MaxPods   int
	MaxCPU    resource.Quantity
	MaxMemory resource.Quantity
}

// EnforcementResult returns current usage and violation state after enforcement attempt.
type EnforcementResult struct {
	CurrentPods   int    `json:"currentPods"`
	CurrentCPU    string `json:"currentCpu"`
	CurrentMemory string `json:"currentMemory"`
	Violation     bool   `json:"violation"`
	Message       string `json:"message"`
}

// PodEnforcer enforces policies per namespace.
type PodEnforcer struct {
	Client      *kubernetes.Clientset
	PolicyCache map[string]Policy // namespace → policy
}

// EnforceUntilOK enforces the policy by deleting pods until usage <= policy or maxIterations reached.
// Returns final usage summary and whether violation still exists.
func (e *PodEnforcer) EnforceUntilOK(namespace string, policy Policy) (EnforcementResult, error) {
	maxIterations := 10 // safety limit
	var lastErr error

	for i := 0; i < maxIterations; i++ {
		res, err := e.computeUsage(namespace, policy)
		if err != nil {
			return EnforcementResult{}, err
		}

		// if no violation -> we're done
		if !res.Violation {
			return res, nil
		}

		// if pods exceed -> delete oldest repeatedly until pods <= max
		pods, err := e.Client.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			lastErr = err
			break
		}
		// If pod deletion required (either due to pod count or resource oversubscription), pick a deletion target.
		target, ok := selectPodToDelete(pods.Items, res.Reason())
		if !ok {
			// nothing to delete => break
			res.Message = "violation but no suitable pod to delete"
			return res, nil
		}

		if delErr := e.Client.CoreV1().Pods(namespace).Delete(context.TODO(), target.Name, metav1.DeleteOptions{}); delErr != nil {
			lastErr = delErr
			log.Printf("failed to delete pod %s/%s: %v", namespace, target.Name, delErr)
			// backoff before retry
			time.Sleep(500 * time.Millisecond)
			continue
		}
		log.Printf("Deleted %s/%s to enforce policy (iteration %d)", namespace, target.Name, i+1)
		// small sleep to let API state converge
		time.Sleep(400 * time.Millisecond)
	}

	// final check
	final, err := e.computeUsage(namespace, policy)
	if err != nil {
		return EnforcementResult{}, err
	}
	return final, lastErr
}

// computeUsage returns an EnforcementResult describing current usage and whether it violates policy.
// This function does not mutate cluster state.
func (e *PodEnforcer) computeUsage(namespace string, policy Policy) (EnforcementResult, error) {
	pods, err := e.Client.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return EnforcementResult{}, fmt.Errorf("list pods: %w", err)
	}

	totalCPU := resource.MustParse("0")
	totalMem := resource.MustParse("0")
	count := 0
	for _, pod := range pods.Items {
		// ignore completed pods
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		count++
		for _, c := range pod.Spec.Containers {
			if cpuReq, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
				totalCPU.Add(cpuReq)
			}
			if memReq, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
				totalMem.Add(memReq)
			}
		}
	}

	// check violations
	violation := false
	msg := ""
	if count > policy.MaxPods {
		violation = true
		msg = fmt.Sprintf("pods:%d>max:%d", count, policy.MaxPods)
	}
	if totalCPU.Cmp(policy.MaxCPU) > 0 {
		violation = true
		msg = fmt.Sprintf("cpu:%s>max:%s", totalCPU.String(), policy.MaxCPU.String())
	}
	if totalMem.Cmp(policy.MaxMemory) > 0 {
		violation = true
		msg = fmt.Sprintf("memory:%s>max:%s", totalMem.String(), policy.MaxMemory.String())
	}

	return EnforcementResult{
		CurrentPods:   count,
		CurrentCPU:    totalCPU.String(),
		CurrentMemory: totalMem.String(),
		Violation:     violation,
		Message:       msg,
	}, nil
}

// selectPodToDelete chooses which pod to delete: oldest if pod count problem, newest if resource oversubscription.
// returns (pod, true) if found, (zero, false) if none.
func selectPodToDelete(pods []corev1.Pod, reason string) (corev1.Pod, bool) {
	if len(pods) == 0 {
		return corev1.Pod{}, false
	}

	if reason == "pods" {
		sort.Slice(pods, func(i, j int) bool {
			return pods[i].CreationTimestamp.Before(&pods[j].CreationTimestamp)
		})
		return pods[0], true
	}
	// else delete newest
	sort.Slice(pods, func(i, j int) bool {
		return pods[i].CreationTimestamp.After(pods[j].CreationTimestamp.Time)
	})
	return pods[0], true
}

// Reason extracts short reason from EnforcementResult.Message (simple parse).
func (r EnforcementResult) Reason() string {
	// message format set above like "pods:12>max:10", "cpu:xxx>max:yyy", etc
	if r.Message == "" {
		return ""
	}
	if len(r.Message) >= 4 && r.Message[:4] == "pods" {
		return "pods"
	}
	if len(r.Message) >= 3 && r.Message[:3] == "cpu" {
		return "cpu"
	}
	if len(r.Message) >= 6 && r.Message[:6] == "memory" {
		return "memory"
	}
	return ""
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
