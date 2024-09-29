package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

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

func GetEnvOrDefault(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func getAnnotationOrDefault(annotations map[string]string, key string, defaultValue string) string {
	logrus.Debugf("Try to get annotations: %s (default: %s)", key, defaultValue)
	if value, exists := annotations[key]; exists {
		logrus.Debugf("Value for annotations: %s = %s)", key, value)
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
				logrus.Errorf("Error retrieving pod %s: %v", podName, err)
				continue
			}

			if isPodHealthy(pod) {
				// Pod est Healthy
				return true
			}

			logrus.Debugf("Waiting for pod %s to be healthy...", podName)
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

// Function to create a unique key combining the image name and tag
func createImageTagKey(image string) string {
	parts := strings.Split(image, ":")
	if len(parts) > 1 {
		return parts[0] + ":" + parts[1] // Combination of image name and tag
	}
	return image
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

	logrus.Debugf("webhookUrl:", webhookUrl)
	// Convertir les paramètres en JSON
	payloadBytes, err := json.Marshal(webhookPayload)
	if err != nil {
		logrus.Errorf("Error marshalling payload to JSON: %v", err)
		return
	}

	// Créer la requête POST avec l'encodage JSON
	req, err := http.NewRequest("POST", webhookUrl, bytes.NewBuffer(payloadBytes))
	if err != nil {
		logrus.Errorf("Error creating webhook request: %v", err)
		return
	}

	// Ajouter les en-têtes nécessaires
	req.Header.Set("Content-Type", "application/json")

	// Envoyer la requête HTTP
	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		logrus.Errorf("Error triggering webhook: %v", err)
		return
	}
	defer resp.Body.Close()

	// Read the response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		logrus.Errorf("Error reading response body: %v", err)
		return
	}

	// Convert the body to a string
	bodyString := string(bodyBytes)

	// Print the response status code and the body of the response
	logrus.Debugf("Webhook triggered with parameters: %v", webhookPayload)
	logrus.Debugf("Response Status Code: %d", resp.StatusCode)
	logrus.Debugf("Response Body: %s", bodyString)
}
