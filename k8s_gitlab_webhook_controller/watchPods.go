package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1" // Import for Pods
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

func watchPods(clientset *kubernetes.Clientset) {
	// Create a watcher for pods
	watchInterface, err := clientset.CoreV1().Pods("").Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}

	logrus.Infof("Watching for pod events...")

	for event := range watchInterface.ResultChan() {
		// Affiche l'événement complet pour debug
		logrus.Debugf("Received event: %v", event)

		pod, ok := event.Object.(*v1.Pod)
		if !ok {
			logrus.Debugf("Event is not a Pod object")
			continue
		}
		logrus.Debugf("event.Type:", event.Type)
		// Affiche le pod reçu pour debug
		logrus.Debugf("Pod received: %s in namespace %s", pod.Name, pod.Namespace)

		switch event.Type {
		case watch.Added:
			logrus.Debugf("Pod %s added in namespace %s", pod.Name, pod.Namespace)

		case watch.Modified:
			logrus.Debugf("Pod %s modified in namespace %s", pod.Name, pod.Namespace)

			if shouldPodTriggerUpdate(pod) {
				// Vérification avec timeout de 70 secondes pour s'assurer que le pod est Healthy
				if isPodHealthyWithTimeout(clientset, pod.Namespace, pod.Name, 70*time.Second) {
					logrus.Debugf("Pod %s is healthy in namespace %s", pod.Name, pod.Namespace)
					processPodImages(pod)
				} else {
					logrus.Debugf("Pod %s is not healthy after 70 seconds in namespace %s", pod.Name, pod.Namespace)
				}
			}

		case watch.Deleted:
			logrus.Debugf("Pod %s deleted in namespace %s", pod.Name, pod.Namespace)
		default:
			logrus.Debugf("Unknown event type: %v", event.Type)

		}
	}
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

			logrus.Infof("Processing image: %s (new) vs %s (old) in pod %s", newImage, oldImage, pod.Name)

			newImageTag := createImageTagKey(newImage)
			oldImageTag := createImageTagKey(oldImage)

			if newImageTag != oldImageTag {
				// Vérifie que la combinaison image + tag n'a pas déjà été traitée
				if !isImageTagProcessed(newImageTag) {
					logrus.Infof("Pod %s updated with new image %s in environment %s", pod.Name, newImage, appEnv)
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
				logrus.Debugf("webhookUrl:", envUrl)
				logrus.Debugf("webhookUrlPath:", envUrlPath)
				logrus.Debugf("PROJECT_ID:", appProjectID)
				webhookUrl := strings.Replace(fmt.Sprintf(`%s%s`, envUrl, envUrlPath), "PROJECT_ID", appProjectID, -1)

				triggerPodsWebhook(webhookUrl, webhookPayload)
			}
		}
	} else {
		logrus.Infof("No container status available for pod %s", pod.Name)
	}
}

// Function to check if the update should trigger based on custom annotation
func shouldPodTriggerUpdate(pod *v1.Pod) bool {
	logrus.Debugf("shouldTriggerUpdate:", pod.Annotations["image.update.trigger"])
	if trigger, exists := pod.Annotations["image.update.trigger"]; exists && trigger == "true" {
		return true
	}
	return false
}

// Function to trigger the webhook with dynamic parameters
func triggerPodsWebhook(webhookUrl string, webhookPayload map[string]string) {

	triggerWebhook(webhookUrl, webhookPayload)

}
