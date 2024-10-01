package main

import (
	"context"

	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

type DeploymentWatcher struct {
	Watcher
}

func (dw *DeploymentWatcher) Start() {
	dw.objTypeName = "Deployment"
	dw.Watcher.Watch("deployments", metav1.ListOptions{}, func() (watch.Interface, error) {
		return dw.Clientset.AppsV1().Deployments("").Watch(context.TODO(), metav1.ListOptions{})
	})
}

func (dw *DeploymentWatcher) HandleEvent(event watch.Event) {
	logrus.Debugf("Handling deployment event...")
	deployment, ok := event.Object.(*appsv1.Deployment)
	if !ok {
		logrus.Debugf("Event is not a Deployment object")
		return
	}
	logrus.Infof("Deployment received: %s for %s in namespace %s", event.Type, deployment.Name, deployment.Namespace)
	switch event.Type {
	case watch.Modified:
		dw.ProcessModified(event, deployment)

	default:
		logrus.Infof("Unhandled event type: %v", event.Type)
	}
}

func (dw *DeploymentWatcher) ProcessModified(event watch.Event, deployment *appsv1.Deployment) {
	logrus.Infof("%s %s modified in namespace %s", dw.objTypeName, deployment.Name, deployment.Namespace)
	if dw.Watcher.shouldTriggerUpdate(deployment) {
		// Check for image update in the first container
		if len(deployment.Spec.Template.Spec.Containers) > 0 {
			newImage := deployment.Spec.Template.Spec.Containers[0].Image

			// Retrieve old image from container statuses if available
			if len(deployment.Status.Conditions) > 0 {
				oldImage := deployment.Status.Conditions[0].Message // Assuming the old image is tracked in conditions (depends on actual usage)
				webhookUrl, webhookPayload, err := dw.processNewImage(newImage, oldImage, deployment)
				if err != nil {
					logrus.Errorf("Failed to process new image: %v", err)
				} else if webhookUrl != "" && webhookPayload != nil {
					logrus.Infof("Webhook triggered: %s with payload %v", webhookUrl, webhookPayload)

					dw.TriggerWebhook(webhookUrl, webhookPayload)
				}
			} else {
				logrus.Errorf("No old image information available for deployment %s", deployment.Name)
			}
		}
	}
}
