package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1" // Import for Deployments
	v1 "k8s.io/api/core/v1"     // Import for Pods
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Global map to track already processed image + tag combinations
var processedTags = make(map[string]bool)
var mutex = &sync.Mutex{}

func main() {
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

	// Watch for changes in pods across all namespaces
	watchDeployments(clientset)
}

func initLogger() {
	// Récupérer le niveau de log depuis la variable d'environnement
	logLevel := strings.ToLower(os.Getenv("LOG_LEVEL"))

	// Définir le niveau de log en fonction de la variable d'environnement
	switch logLevel {
	case "debug":
		logrus.SetLevel(logrus.DebugLevel)
	case "info":
		logrus.SetLevel(logrus.InfoLevel)
	case "error":
		logrus.SetLevel(logrus.ErrorLevel)
	default:
		logrus.SetLevel(logrus.InfoLevel) // Niveau par défaut
	}

	// Format de sortie en texte lisible
	logrus.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})
}

func watchPods(clientset *kubernetes.Clientset) {
	// Create a watcher for pods
	watchInterface, err := clientset.CoreV1().Pods("").Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}

	logrus.Info("Watching for pod events...")

	for event := range watchInterface.ResultChan() {
		// Affiche l'événement complet pour debug
		logrus.Debug("Received event: %v\n", event)

		pod, ok := event.Object.(*v1.Pod)
		if !ok {
			logrus.Debug("Event is not a Pod object")
			continue
		}
		logrus.Debug("event.Type:", event.Type)
		// Affiche le pod reçu pour debug
		logrus.Debug("Pod received: %s in namespace %s\n", pod.Name, pod.Namespace)

		switch event.Type {
		case watch.Added:
			logrus.Debug("Pod %s added in namespace %s\n", pod.Name, pod.Namespace)

		case watch.Modified:
			logrus.Debug("Pod %s modified in namespace %s\n", pod.Name, pod.Namespace)

			if shouldPodTriggerUpdate(pod) {
				// Vérification avec timeout de 70 secondes pour s'assurer que le pod est Healthy
				if isPodHealthyWithTimeout(clientset, pod.Namespace, pod.Name, 70*time.Second) {
					logrus.Debug("Pod %s is healthy in namespace %s\n", pod.Name, pod.Namespace)
					processPodImages(pod)
				} else {
					logrus.Debug("Pod %s is not healthy after 70 seconds in namespace %s\n", pod.Name, pod.Namespace)
				}
			}

		case watch.Deleted:
			logrus.Debug("Pod %s deleted in namespace %s\n", pod.Name, pod.Namespace)
		default:
			logrus.Debug("Unknown event type: %v\n", event.Type)

		}
	}
}

func GetEnvOrDefault(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func getAnnotationOrDefault(annotations map[string]string, key string, defaultValue string) string {
	logrus.Debug("Try to get annotations: %s (default: %s)\n", key, defaultValue)
	if value, exists := annotations[key]; exists {
		logrus.Debug("Value for annotations: %s = %s)\n", key, value)
		return value
	}
	return defaultValue
}

// Fonction pour vérifier si le pod est Healthy avec un timeout maximum
func isPodHealthyWithTimeout(clientset *kubernetes.Clientset, namespace, podName string, timeout time.Duration) bool {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeoutChan := time.After(timeout)
	for {
		select {
		case <-timeoutChan:
			// Timeout atteint, on considère que le pod n'est pas healthy
			return false
		case <-ticker.C:
			// Vérification régulière du statut Healthy
			pod, err := clientset.CoreV1().Pods(namespace).Get(context.TODO(), podName, metav1.GetOptions{})
			if err != nil {
				logrus.Error("Error retrieving pod %s: %v\n", podName, err)
				continue
			}

			if isPodHealthy(pod) {
				// Pod est Healthy
				return true
			}

			logrus.Debug("Waiting for pod %s to be healthy...\n", podName)
		}
	}
}

// Function to check if the pod is healthy
func isPodHealthy(pod *v1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == v1.PodReady && condition.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

// Function to extract the tag from the image name (after the last colon `:`)
func extractImageTag(image string) string {
	parts := strings.Split(image, ":")
	if len(parts) > 1 {
		return parts[1] // Returns the tag part
	}
	return "latest" // Default tag if no tag is provided (assuming "latest")
}

// Exemple de traitement des images des pods
func processPodImages(pod *v1.Pod) {
	if len(pod.Status.ContainerStatuses) > 0 {

		appEnv := getAnnotationOrDefault(pod.Annotations, "config.app/env", "default")
		appBranch := getAnnotationOrDefault(pod.Annotations, "config.app/branch", "default")
		appProjectID := getAnnotationOrDefault(pod.Annotations, "config.app/project-id", "123456")
		authToken := os.Getenv("AUTH_TOKEN")
		imageTags := []string{}

		for _, container := range pod.Spec.Containers {
			newImage := container.Image
			oldImage := pod.Status.ContainerStatuses[0].Image

			logrus.Info("Processing image: %s (new) vs %s (old) in pod %s\n", newImage, oldImage, pod.Name)

			newImageTag := createImageTagKey(newImage)
			oldImageTag := createImageTagKey(oldImage)

			if newImageTag != oldImageTag {
				// Vérifie que la combinaison image + tag n'a pas déjà été traitée
				if !isImageTagProcessed(newImageTag) {
					logrus.Info("Pod %s updated with new image %s in environment %s\n", pod.Name, newImage, appEnv)
					imageTags = append(imageTags, newImageTag)

					// Marquer cette image + tag comme traitée
					markImageTagAsProcessed(newImageTag)
				}
			}
			// If there are any new image tags, trigger the webhook
			if len(imageTags) > 0 {

				webhookPayload := map[string]string{
					"ref":                      appBranch,
					"token":                    authToken,
					"variables[TRIGGERED_ENV]": appEnv,
					"variables[IMAGE_TAG]":     imageTags[0],
				}

				envUrl := GetEnvOrDefault("URL", "https://gitlab.com/api/v4")
				envUrlPath := GetEnvOrDefault("URL_PATH", "/projects/PROJECT_ID/trigger/pipeline")
				logrus.Debug("webhookUrl:", envUrl)
				logrus.Debug("webhookUrlPath:", envUrlPath)
				logrus.Debug("PROJECT_ID:", appProjectID)
				webhookUrl := strings.Replace(fmt.Sprintf(`%s%s`, envUrl, envUrlPath), "PROJECT_ID", appProjectID, -1)
				logrus.Debug("webhookUrl:", webhookUrl)
				triggerWebhook(webhookUrl, webhookPayload)
			}
		}
	} else {
		logrus.Info("No container status available for pod %s\n", pod.Name)
	}
}

// Function to create a unique key combining the image name and tag
func createImageTagKey(image string) string {
	parts := strings.Split(image, ":")
	if len(parts) > 1 {
		return parts[0] + ":" + parts[1] // Combination of image name and tag
	}
	return image
}

// Function to check if the update should trigger based on custom annotation
func shouldPodTriggerUpdate(pod *v1.Pod) bool {
	logrus.Debug("shouldTriggerUpdate:", pod.Annotations["image.update.trigger"])
	if trigger, exists := pod.Annotations["image.update.trigger"]; exists && trigger == "true" {
		return true
	}
	return false
}
func shouldDeploymentTriggerUpdate(deployment *appsv1.Deployment) bool {
	logrus.Debug("shouldTriggerUpdate:", deployment.Annotations["image.update.trigger"])
	if trigger, exists := deployment.Annotations["image.update.trigger"]; exists && trigger == "true" {
		return true
	}
	return false
}

// Check if a combination of image+tag has already been processed
func isImageTagProcessed(imageTag string) bool {
	mutex.Lock()
	defer mutex.Unlock()
	_, processed := processedTags[imageTag]
	return processed
}

// Mark a combination of image+tag as processed
func markImageTagAsProcessed(imageTag string) {
	mutex.Lock()
	defer mutex.Unlock()
	processedTags[imageTag] = true
}

// Function to trigger the webhook with dynamic parameters
func triggerWebhook(webhookUrl string, webhookPayload map[string]string) {

	// Convertir les paramètres en JSON
	payloadBytes, err := json.Marshal(webhookPayload)
	if err != nil {
		logrus.Error("Error marshalling payload to JSON: %v\n", err)
		return
	}

	// Créer la requête POST avec l'encodage JSON
	req, err := http.NewRequest("POST", webhookUrl, bytes.NewBuffer(payloadBytes))
	if err != nil {
		logrus.Error("Error creating webhook request: %v\n", err)
		return
	}

	// Ajouter les en-têtes nécessaires
	req.Header.Set("Content-Type", "application/json")

	// Envoyer la requête HTTP
	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		logrus.Error("Error triggering webhook: %v\n", err)
		return
	}
	defer resp.Body.Close()

	// Read the response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		logrus.Error("Error reading response body: %v\n", err)
		return
	}

	// Convert the body to a string
	bodyString := string(bodyBytes)

	// Print the response status code and the body of the response
	logrus.Debug("Webhook triggered with parameters: %v\n", webhookPayload)
	logrus.Debug("Response Status Code: %d\n", resp.StatusCode)
	logrus.Debug("Response Body: %s\n", bodyString)
}

// Function to watch deployment changes
func watchDeployments(clientset *kubernetes.Clientset) {
	watchInterface, err := clientset.AppsV1().Deployments("").Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}

	logrus.Info("Watching for deployment events...")

	for event := range watchInterface.ResultChan() {
		// Debug print to display the event received
		logrus.Debug("Received event: %v\n", event)

		deployment, ok := event.Object.(*appsv1.Deployment)
		if !ok {
			logrus.Debug("Event is not a Deployment object")
			continue
		}

		logrus.Info("Deployment received: %s in namespace %s\n", deployment.Name, deployment.Namespace)

		switch event.Type {
		case watch.Modified:
			logrus.Info("Deployment %s modified in namespace %s\n", deployment.Name, deployment.Namespace)
			if shouldDeploymentTriggerUpdate(deployment) {
				// Check for image update in the first container
				if len(deployment.Spec.Template.Spec.Containers) > 0 {
					newImage := deployment.Spec.Template.Spec.Containers[0].Image

					// Retrieve old image from container statuses if available
					if len(deployment.Status.Conditions) > 0 {
						oldImage := deployment.Status.Conditions[0].Message // Assuming the old image is tracked in conditions (depends on actual usage)

						newImageTag := createImageTagKey(newImage)
						oldImageTag := createImageTagKey(oldImage)

						if newImageTag != oldImageTag {
							// Check if this image + tag combination has already been processed
							if !isImageTagProcessed(newImageTag) {
								logrus.Debug("Deployment %s updated with new image %s in namespace %s\n", deployment.Name, newImage, deployment.Namespace)

								// Mark this image + tag combination as processed
								markImageTagAsProcessed(newImageTag)
								appEnv := getAnnotationOrDefault(deployment.Annotations, "config.app/env", "default")
								appBranch := getAnnotationOrDefault(deployment.Annotations, "config.app/branch", "default")
								appProjectID := getAnnotationOrDefault(deployment.Annotations, "config.app/project-id", "123456")
								authToken := os.Getenv("AUTH_TOKEN")

								// Extract the tag from the image
								imageTag := extractImageTag(newImage)
								// Trigger the webhook
								webhookPayload := map[string]string{
									"ref":                      appBranch,
									"token":                    authToken,
									"variables[TRIGGERED_ENV]": appEnv,
									"variables[IMAGE_TAG]":     imageTag,
								}
								envUrl := GetEnvOrDefault("URL", "https://gitlab.com/api/v4")
								envUrlPath := GetEnvOrDefault("URL_PATH", "/projects/PROJECT_ID/trigger/pipeline")
								logrus.Debug("webhookUrl:", envUrl)
								logrus.Debug("webhookUrlPath:", envUrlPath)
								logrus.Debug("PROJECT_ID:", appProjectID)
								webhookUrl := strings.Replace(fmt.Sprintf(`%s%s`, envUrl, envUrlPath), "PROJECT_ID", appProjectID, -1)
								logrus.Debug("webhookUrl:", webhookUrl)
								triggerWebhook(webhookUrl, webhookPayload)

							}
						}
					} else {
						logrus.Error("No old image information available for deployment %s\n", deployment.Name)
					}
				}
			}
		default:
			logrus.Info("Unhandled event type: %v\n", event.Type)
		}
	}
}
