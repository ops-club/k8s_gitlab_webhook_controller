package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1" // Import for Deployments
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
)

// Function to watch deployment changes
func watchDeployments(clientset *kubernetes.Clientset) {
	watchInterface, err := clientset.AppsV1().Deployments("").Watch(context.TODO(), metav1.ListOptions{})
	if err != nil {
		panic(err.Error())
	}

	logrus.Infof("Watching for deployment events...")

	for event := range watchInterface.ResultChan() {
		// Debug print to display the event received
		logrus.Debugf("Received event: %v", event)

		deployment, ok := event.Object.(*appsv1.Deployment)
		if !ok {
			logrus.Debugf("Event is not a Deployment object")
			continue
		}

		logrus.Infof("Deployment received: %s in namespace %s", deployment.Name, deployment.Namespace)

		switch event.Type {
		case watch.Modified:
			logrus.Infof("Deployment %s modified in namespace %s", deployment.Name, deployment.Namespace)
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
								logrus.Debugf("Deployment %s updated with new image %s in namespace %s", deployment.Name, newImage, deployment.Namespace)

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
								logrus.Debugf("webhookUrl:", envUrl)
								logrus.Debugf("webhookUrlPath:", envUrlPath)
								logrus.Debugf("PROJECT_ID:", appProjectID)
								webhookUrl := strings.Replace(fmt.Sprintf(`%s%s`, envUrl, envUrlPath), "PROJECT_ID", appProjectID, -1)

								triggerDeploymentsWebhook(webhookUrl, webhookPayload)

							}
						}
					} else {
						logrus.Errorf("No old image information available for deployment %s", deployment.Name)
					}
				}
			}
		default:
			logrus.Infof("Unhandled event type: %v", event.Type)
		}
	}
}

func shouldDeploymentTriggerUpdate(deployment *appsv1.Deployment) bool {
	logrus.Debugf("shouldTriggerUpdate:", deployment.Annotations["image.update.trigger"])
	if trigger, exists := deployment.Annotations["image.update.trigger"]; exists && trigger == "true" {
		return true
	}
	return false
}

// Function to trigger the webhook with dynamic parameters
func triggerDeploymentsWebhook(webhookUrl string, webhookPayload map[string]string) {

	triggerWebhook(webhookUrl, webhookPayload)

}
