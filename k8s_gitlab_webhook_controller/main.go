package main

import (
	"flag"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Global map to track already processed image + tag combinations
var processedTags = make(map[string]bool)
var mutex = &sync.Mutex{}

func main() {

	watchDeployments := flag.Bool("deployments", false, "Enable watch on 'deployments'")
	watchStatefulSets := flag.Bool("statefulsets", true, "Enable watch on 'statefulsets'")
	watchPods := flag.Bool("pods", false, "Enable watch on 'pods'")
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

	// Initialize the watchers
	deploymentWatcher := &DeploymentWatcher{
		Watcher: Watcher{Clientset: clientset},
	}

	podWatcher := &PodWatcher{
		Watcher: Watcher{Clientset: clientset},
	}

	statefulSetWatcher := &StatefulSetWatcher{
		Watcher: Watcher{Clientset: clientset},
	}

	// Start watching resources
	if *watchDeployments {
		go deploymentWatcher.Start() // Watch for Deployment events
	}
	if *watchStatefulSets {
		go statefulSetWatcher.Start() // Watch for StatefulSet events
	}
	if *watchPods {
		go podWatcher.Start() // Watch for Pod events
	}

	// Keep the main function running
	select {} // This is to keep the main goroutine alive

}
