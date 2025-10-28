package crdclient

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func GetDynamicClient() (dynamic.Interface, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		// fallback for local testing
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}

	return dynamic.NewForConfig(config)
}
