package main

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

type PodWatcher struct {
	Watcher
}

func (pw *PodWatcher) Start() {
	pw.objTypeName = "Pod"
	pw.Watcher.Watch("pods", metav1.ListOptions{}, func() (watch.Interface, error) {
		return pw.Clientset.CoreV1().Pods("").Watch(context.TODO(), metav1.ListOptions{})
	})
}

func (pw *PodWatcher) HandleEvent(event watch.Event) {
	logrus.Debugf("Handling pod event...")
	pod, ok := event.Object.(*v1.Pod)
	if !ok {
		logrus.Debugf("Event is not a Pod object")
		return
	}
	logrus.Infof("Pod received: %s in namespace %s", pod.Name, pod.Namespace)
	switch event.Type {
	case watch.Modified:
		pw.ProcessModified(event, pod)

	default:
		logrus.Infof("Unhandled event type: %v", event.Type)
	}
}

func (pw *PodWatcher) ProcessModified(event watch.Event, pod *v1.Pod) {
	logrus.Infof("%s %s modified in namespace %s", pw.objTypeName, pod.Name, pod.Namespace)
	if pw.Watcher.shouldTriggerUpdate(pod) {
		if isPodHealthyWithTimeout(pw.Clientset, pod.Namespace, pod.Name, 70*time.Second) {
			logrus.Debugf("Pod %s is healthy in namespace %s", pod.Name, pod.Namespace)

			if len(pod.Status.ContainerStatuses) > 0 {
				for _, container := range pod.Spec.Containers {
					newImage := container.Image
					oldImage := pod.Status.ContainerStatuses[0].Image

					logrus.Infof("Processing image: %s (new) vs %s (old) in pod %s", newImage, oldImage, pod.Name)
					webhookUrl, webhookPayload, err := pw.processNewImage(newImage, oldImage, pod)
					if err != nil {
						logrus.Errorf("Failed to process new image: %v", err)
					} else if webhookUrl != "" && webhookPayload != nil {
						logrus.Infof("Webhook triggered: %s with payload %v", webhookUrl, webhookPayload)

						pw.TriggerWebhook(webhookUrl, webhookPayload)
					}
				}
			} else {
				logrus.Errorf("No old image information available for pod %s", pod.Name)
			}

		} else {
			logrus.Debugf("Pod %s is not healthy after 70 seconds in namespace %s", pod.Name, pod.Namespace)
		}
	}
}
