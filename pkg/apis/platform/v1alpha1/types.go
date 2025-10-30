package v1alpha1

// +kubebuilder:object:generate=true

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceQuotaPolicySpec defines the desired state
type ResourceQuotaPolicySpec struct {
	MaxPods   int    `json:"maxPods,omitempty"`
	MaxCPU    string `json:"maxCPU,omitempty"`
	MaxMemory string `json:"maxMemory,omitempty"`
}

// ResourceQuotaPolicyStatus defines observed usage
type ResourceQuotaPolicyStatus struct {
	CurrentPods int      `json:"currentPods,omitempty"`
	CPUUsage    string   `json:"cpuUsage,omitempty"`
	MemoryUsage string   `json:"memoryUsage,omitempty"`
	Violations  []string `json:"violations,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ResourceQuotaPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ResourceQuotaPolicySpec   `json:"spec,omitempty"`
	Status ResourceQuotaPolicyStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ResourceQuotaPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResourceQuotaPolicy `json:"items"`
}
