package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

type Watcher struct {
	Clientset                            *kubernetes.Clientset
	KubernetesAnnotationWatcherEnable    string
	KubernetesAnnotationWatcherEnv       string
	KubernetesAnnotationWatcherBranch    string
	KubernetesAnnotationWatcherProjectID string
	objTypeName                          string
}

func (w *Watcher) Watch(resource string, listOptions metav1.ListOptions, getWatchFunc func() (watch.Interface, error)) {
	w.KubernetesAnnotationWatcherEnable = "image.update.trigger"
	w.KubernetesAnnotationWatcherEnv = "config.app/env"
	w.KubernetesAnnotationWatcherBranch = "config.app/branch"
	w.KubernetesAnnotationWatcherProjectID = "config.app/project-id"
	watchInterface, err := getWatchFunc()
	if err != nil {
		logrus.Fatalf("Failed to start watch on %s: %v", resource, err)
	}
	logrus.Infof("Watching for %s events...", resource)

	for event := range watchInterface.ResultChan() {
		w.HandleEvent(event)
	}
}

func (w *Watcher) HandleEvent(event watch.Event) {
	logrus.Debugf("Received event: %v", event)
	// This method can be overridden by specific watchers
}

// Function to trigger the webhook with dynamic parameters
func (w *Watcher) TriggerWebhook(webhookUrl string, webhookPayload map[string]string) {

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

func (w *Watcher) shouldTriggerUpdate(obj metav1.Object) bool {
	logrus.Debugf("shouldTriggerUpdate: %s", obj.GetAnnotations()[w.KubernetesAnnotationWatcherEnable])
	if trigger, exists := obj.GetAnnotations()[w.KubernetesAnnotationWatcherEnable]; exists && trigger == "true" {
		return true
	}
	return false
}

// processNewImage checks if a new image should trigger an update, processes it, and returns the webhook URL and payload.
func (w *Watcher) processNewImage(newImage string, oldImage string, obj metav1.Object) (string, map[string]string, error) {

	newImageTag := createImageTagKey(newImage)
	oldImageTag := createImageTagKey(oldImage)

	if newImageTag != oldImageTag {
		// Check if this image + tag combination has already been processed
		if !isImageTagProcessed(newImageTag) {
			logrus.Debugf("%s %s updated with new image %s in namespace %s", w.objTypeName, obj.GetName(), newImage, obj.GetNamespace())

			// Mark this image + tag combination as processed
			markImageTagAsProcessed(newImageTag)
			// Extract annotations using GetAnnotations() method
			annotations := obj.GetAnnotations()

			appEnv := getAnnotationOrDefault(annotations, w.KubernetesAnnotationWatcherEnv, "default")
			appBranch := getAnnotationOrDefault(annotations, w.KubernetesAnnotationWatcherBranch, "default")
			appProjectID := getAnnotationOrDefault(annotations, w.KubernetesAnnotationWatcherProjectID, "123456")
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
			logrus.Debugf("webhookUrl:", envUrl)
			logrus.Debugf("webhookUrlPath:", envUrlPath)
			logrus.Debugf("PROJECT_ID:", appProjectID)
			webhookUrl := strings.Replace(fmt.Sprintf(`%s%s`, envUrl, envUrlPath), "PROJECT_ID", appProjectID, -1)

			return webhookUrl, webhookPayload, nil
		}
	}
	// If no new image or the tag is already processed, return empty values
	return "", nil, nil
}
