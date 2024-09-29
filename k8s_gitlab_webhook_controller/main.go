package main

import (
	"flag"
	"log"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Global map to track already processed image + tag combinations
var processedTags = make(map[string]bool)
var mutex = &sync.Mutex{}

func main() {
	// Paramètre pour choisir entre surveiller les déploiements ou les pods
	mode := flag.String("mode", "deployments", "Mode to watch: 'deployments' or 'pods'")
	flag.Parse()

	// Initialiser le logger
	initLogger()

	// Create Kubernetes in-cluster configuration
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err.Error())
	}

	// Create clientset to interact with Kubernetes
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	// Sélection de la fonction en fonction du paramètre mode
	switch *mode {
	case "deployments":
		watchDeployments(clientset)
	case "pods":
		watchPods(clientset)
	default:
		log.Fatalf("Invalid mode: %s. Use 'deployments' or 'pods'", *mode)
	}
}
