package main

import (
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "strings"
    "bytes"
    "context"
    "sync"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
    "k8s.io/apimachinery/pkg/watch"
    v1 "k8s.io/api/core/v1"
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
    watchPods(clientset)
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
            
            if shouldTriggerUpdate(pod) {
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


// Fonction pour vérifier si le pod est Healthy (utilisé dans le timeout)
func isPodHealthy(pod *v1.Pod) bool {
    for _, condition := range pod.Status.Conditions {
        if condition.Type == v1.PodReady && condition.Status == v1.ConditionTrue {
            return true
        }
    }
    return false
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
                webhookParams := []string{
                    fmt.Sprintf("ref=%s", appBranch),
                    fmt.Sprintf("token=%s", authToken),
                    fmt.Sprintf("variables[TRIGGERED_ENV]=%s", appEnv),
                    fmt.Sprintf("variables[IMAGE_TAG]=%s", imageTags[0]), // Assuming we send the first tag, modify if needed
                }
                envUrl := GetEnvOrDefault("URL", "https://gitlab.com/api/v4")
                envUrlPath := GetEnvOrDefault("URL_PATH", "/projects/PROJECT_ID/trigger/pipeline")
                fmt.Println("webhookUrl:", envUrl)
                fmt.Println("webhookUrlPath:", envUrlPath)
                fmt.Println("PROJECT_ID:", appProjectID)
                webhookUrl := strings.Replace(fmt.Sprintf(`%s%s`, envUrl, envUrlPath), "PROJECT_ID", appProjectID, -1)
                triggerWebhook(webhookUrl, webhookParams)
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
func shouldTriggerUpdate(pod *v1.Pod) bool {
    fmt.Println("shouldTriggerUpdate:", pod.Annotations["image.update.trigger"])
    if trigger, exists := pod.Annotations["image.update.trigger"]; exists && trigger == "true" {
        return true
    }
    return false
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
func triggerWebhook(webhookUrl string, webhookParams []string) {

    // Convertir les paramètres en JSON
    payloadBytes, err := json.Marshal(webhookParams)
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
    } else {
        defer resp.Body.Close()
        fmt.Printf("Webhook triggered with parameters: %v\n", webhookParams)
    }
}
