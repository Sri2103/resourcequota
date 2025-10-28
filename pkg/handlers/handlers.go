package handlers

import (
	"context"
	"log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type PodEnforcer struct {
	Client      *kubernetes.Clientset
	PolicyCache map[string]int
}

func (e *PodEnforcer) Enforce(namespace string) {
	pods, err := e.Client.CoreV1().Pods(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		log.Printf("❌ Error listing pods in %s: %v", namespace, err)
		return
	}

	max, exist := e.PolicyCache[namespace]

	if !exist {
		return
	}

	if len(pods.Items) > max {
		log.Printf("⚠️ Namespace %s exceeds Pod quota (%d/%d). Deleting oldest pod...", namespace, len(pods.Items), max)
		oldest := pods.Items[0]

		for _, p := range pods.Items {
			if p.CreationTimestamp.Before(&oldest.CreationTimestamp) {
				oldest = p
			}
		}

		err := e.Client.CoreV1().Pods(namespace).Delete(context.TODO(), oldest.Name, metav1.DeleteOptions{})

		if err != nil {
			log.Printf("❌ Failed to delete pod %s/%s: %v", namespace, oldest.Name, err)
		} else {
			log.Printf("🗑️ Deleted pod %s/%s to enforce quota", namespace, oldest.Name)
		}

	}
}
