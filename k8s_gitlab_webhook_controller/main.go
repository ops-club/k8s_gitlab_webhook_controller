package main

import (
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "io"
    "strings"
    "bytes"
    "context"
    "sync"
    "time"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
    "k8s.io/apimachinery/pkg/watch"
    v1 "k8s.io/api/core/v1" // Import for Pods
    appsv1 "k8s.io/api/apps/v1" // Import for Deployments
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Global map to track already processed image + tag combinations
var processedTags = make(map[string]bool)
var mutex = &sync.Mutex{}

func main() {
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

func watchPods(clientset *kubernetes.Clientset) {
    // Create a watcher for pods
    watchInterface, err := clientset.CoreV1().Pods("").Watch(context.TODO(), metav1.ListOptions{})
    if err != nil {
        panic(err.Error())
    }

    fmt.Println("Watching for pod events...")

    for event := range watchInterface.ResultChan() {
        // Affiche l'événement complet pour debug
        fmt.Printf("Received event: %v\n", event)

        pod, ok := event.Object.(*v1.Pod)
        if !ok {
            fmt.Println("Event is not a Pod object")
            continue
        }
        fmt.Println("event.Type:", event.Type)
        // Affiche le pod reçu pour debug
        fmt.Printf("Pod received: %s in namespace %s\n", pod.Name, pod.Namespace)

        switch event.Type {
        case watch.Added:
            fmt.Printf("Pod %s added in namespace %s\n", pod.Name, pod.Namespace)
        
        case watch.Modified:
            fmt.Printf("Pod %s modified in namespace %s\n", pod.Name, pod.Namespace)
            
            if shouldPodTriggerUpdate(pod) {
                // Vérification avec timeout de 70 secondes pour s'assurer que le pod est Healthy
                if isPodHealthyWithTimeout(clientset, pod.Namespace, pod.Name, 70*time.Second) {
                    fmt.Printf("Pod %s is healthy in namespace %s\n", pod.Name, pod.Namespace)
                    processPodImages(pod)
                } else {
                    fmt.Printf("Pod %s is not healthy after 70 seconds in namespace %s\n", pod.Name, pod.Namespace)
                }
            }
        
        case watch.Deleted:
            fmt.Printf("Pod %s deleted in namespace %s\n", pod.Name, pod.Namespace)
        default:
            fmt.Printf("Unknown event type: %v\n", event.Type)
        
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
    if value, exists := annotations[key]; exists {
        return value
        // if strValue, ok := value.(string); ok {
            // return strValue
        // }
        // return fmt.Sprintf("%v", value)
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
                fmt.Printf("Error retrieving pod %s: %v\n", podName, err)
                continue
            }

            if isPodHealthy(pod) {
                // Pod est Healthy
                return true
            }

            fmt.Printf("Waiting for pod %s to be healthy...\n", podName)
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

            fmt.Printf("Processing image: %s (new) vs %s (old) in pod %s\n", newImage, oldImage, pod.Name)

            newImageTag := createImageTagKey(newImage)
            oldImageTag := createImageTagKey(oldImage)

            if newImageTag != oldImageTag {
                // Vérifie que la combinaison image + tag n'a pas déjà été traitée
                if !isImageTagProcessed(newImageTag) {
                    fmt.Printf("Pod %s updated with new image %s in environment %s\n", pod.Name, newImage, appEnv)
                    imageTags = append(imageTags, newImageTag)

                    // Marquer cette image + tag comme traitée
                    markImageTagAsProcessed(newImageTag)
                }
            }
            // If there are any new image tags, trigger the webhook
            if len(imageTags) > 0 {
                
                webhookPayload := map[string]string{
                    "ref": appBranch
                    "token": authToken,
                    "variables[TRIGGERED_ENV]": appEnv,
                    "variables[IMAGE_TAG]": imageTags[0],
                }
                
                envUrl := GetEnvOrDefault("URL", "https://gitlab.com/api/v4")
                envUrlPath := GetEnvOrDefault("URL_PATH", "/projects/PROJECT_ID/trigger/pipeline")
                fmt.Println("webhookUrl:", envUrl)
                fmt.Println("webhookUrlPath:", envUrlPath)
                fmt.Println("PROJECT_ID:", appProjectID)
                webhookUrl := strings.Replace(fmt.Sprintf(`%s%s`, envUrl, envUrlPath), "PROJECT_ID", appProjectID, -1)
                fmt.Println("webhookUrl:", webhookUrl)
                triggerWebhook(webhookUrl, webhookPayload)
            }
        }
    } else {
        fmt.Printf("No container status available for pod %s\n", pod.Name)
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
    fmt.Println("shouldTriggerUpdate:", pod.Annotations["image.update.trigger"])
    if trigger, exists := pod.Annotations["image.update.trigger"]; exists && trigger == "true" {
        return true
    }
    return false
}
func shouldDeploymentTriggerUpdate(deployment *appsv1.Deployment) bool {
    fmt.Println("shouldTriggerUpdate:", deployment.Annotations["image.update.trigger"])
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
        fmt.Printf("Error marshalling payload to JSON: %v\n", err)
        return
    }

    // Créer la requête POST avec l'encodage JSON
    req, err := http.NewRequest("POST", webhookUrl, bytes.NewBuffer(payloadBytes))
    if err != nil {
        fmt.Printf("Error creating webhook request: %v\n", err)
        return
    }

    // Ajouter les en-têtes nécessaires
    req.Header.Set("Content-Type", "application/json")

    // Envoyer la requête HTTP
    client := &http.Client{}
    resp, err := client.Do(req)

    if err != nil {
        fmt.Printf("Error triggering webhook: %v\n", err)
        return
    }
    defer resp.Body.Close()
    
    // Read the response body
    bodyBytes, err := io.ReadAll(resp.Body)
    if err != nil {
        fmt.Printf("Error reading response body: %v\n", err)
        return
    }
    
    // Convert the body to a string
    bodyString := string(bodyBytes)
    
    // Print the response status code and the body of the response
    fmt.Printf("Webhook triggered with parameters: %v\n", webhookParams)
    fmt.Printf("Response Status Code: %d\n", resp.StatusCode)
    fmt.Printf("Response Body: %s\n", bodyString)
}

// Function to watch deployment changes
func watchDeployments(clientset *kubernetes.Clientset) {
    watchInterface, err := clientset.AppsV1().Deployments("").Watch(context.TODO(), metav1.ListOptions{})
    if err != nil {
        panic(err.Error())
    }

    fmt.Println("Watching for deployment events...")

    for event := range watchInterface.ResultChan() {
        // Debug print to display the event received
        fmt.Printf("Received event: %v\n", event)

        deployment, ok := event.Object.(*appsv1.Deployment)
        if !ok {
            fmt.Println("Event is not a Deployment object")
            continue
        }

        fmt.Printf("Deployment received: %s in namespace %s\n", deployment.Name, deployment.Namespace)

        switch event.Type {
        case watch.Modified:
            fmt.Printf("Deployment %s modified in namespace %s\n", deployment.Name, deployment.Namespace)
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
                                fmt.Printf("Deployment %s updated with new image %s in namespace %s\n", deployment.Name, newImage, deployment.Namespace)

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
                                    "ref": appBranch
                                    "token": authToken,
                                    "variables[TRIGGERED_ENV]": appEnv,
                                    "variables[IMAGE_TAG]": imageTag,
                                }
                                envUrl := GetEnvOrDefault("URL", "https://gitlab.com/api/v4")
                                envUrlPath := GetEnvOrDefault("URL_PATH", "/projects/PROJECT_ID/trigger/pipeline")
                                fmt.Println("webhookUrl:", envUrl)
                                fmt.Println("webhookUrlPath:", envUrlPath)
                                fmt.Println("PROJECT_ID:", appProjectID)
                                webhookUrl := strings.Replace(fmt.Sprintf(`%s%s`, envUrl, envUrlPath), "PROJECT_ID", appProjectID, -1)
                                fmt.Println("webhookUrl:", webhookUrl)
                                triggerWebhook(webhookUrl, webhookPayload)
                                
                            }
                        }
                    } else {
                        fmt.Printf("No old image information available for deployment %s\n", deployment.Name)
                    }
                }
            }
        default:
            fmt.Printf("Unhandled event type: %v\n", event.Type)
        }
    }
}
