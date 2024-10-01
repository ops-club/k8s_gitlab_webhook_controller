package main

import (
	"context"

	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

type StatefulSetWatcher struct {
	Watcher
}

func (sw *StatefulSetWatcher) Start() {
	sw.objTypeName = "StatefulSet"
	sw.Watcher.Watch("statefulsets", metav1.ListOptions{}, func() (watch.Interface, error) {
		return sw.Clientset.AppsV1().StatefulSets("").Watch(context.TODO(), metav1.ListOptions{})
	})
}

func (sw *StatefulSetWatcher) HandleEvent(event watch.Event) {
	logrus.Debugf("Handling StatefulSet event...")
	statefulSet, ok := event.Object.(*appsv1.StatefulSet)
	if !ok {
		logrus.Debugf("Event is not a StatefulSet object")
		return
	}
	logrus.Infof("StatefulSet received: %s for %s in namespace %s", event.Type, statefulSet.Name, statefulSet.Namespace)
	switch event.Type {
	case watch.Modified:
		sw.ProcessModified(event, statefulSet)

	default:
		logrus.Infof("Unhandled event type: %v", event.Type)
	}

}

func (sw *StatefulSetWatcher) ProcessModified(event watch.Event, statefulSet *appsv1.StatefulSet) {
	logrus.Infof("%s %s modified in namespace %s", sw.objTypeName, statefulSet.Name, statefulSet.Namespace)
	if sw.Watcher.shouldTriggerUpdate(statefulSet) {
		// Check for image update in the first container
		if len(statefulSet.Spec.Template.Spec.Containers) > 0 {
			newImage := statefulSet.Spec.Template.Spec.Containers[0].Image

			// Retrieve old image from container statuses if available
			if len(statefulSet.Status.Conditions) > 0 {
				oldImage := statefulSet.Status.Conditions[0].Message // Assuming the old image is tracked in conditions (depends on actual usage)

				webhookUrl, webhookPayload, err := sw.processNewImage(newImage, oldImage, statefulSet)
				if err != nil {
					logrus.Errorf("Failed to process new image: %v", err)
				} else if webhookUrl != "" && webhookPayload != nil {
					logrus.Infof("Webhook triggered: %s with payload %v", webhookUrl, webhookPayload)

					sw.TriggerWebhook(webhookUrl, webhookPayload)
				}

			} else {
				logrus.Errorf("No old image information available for statefulSet %s", statefulSet.Name)
			}
		}
	}
}
