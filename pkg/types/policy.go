package types

type ResourceQuotaPolicySpec struct {
	MaxCPU    string `json:"maxCPU"`
	MaxMemory string `json:"maxMemory"`
	MaxPods   int    `json:"maxPods"`
}

type ResourceQuotaPolicy struct {
	Spec ResourceQuotaPolicySpec `json:"spec"`
}
