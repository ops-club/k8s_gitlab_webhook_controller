set dotenv-load := true
export IMAGE_NAME := env_var_or_default("IMAGE_NAME", "opsclub/k8s_gitlab_webhook_controller")
export IMAGE_TAG := env_var_or_default("IMAGE_TAG", "latest")

full: build push deploy deploy-chek
    @echo "Done"

build:
    docker --debug build --platform linux/arm64 -t $IMAGE_NAME:$IMAGE_TAG k8s_gitlab_webhook_controller
    
push:
    docker push $IMAGE_NAME:$IMAGE_TAG

deploy:
    kubectl apply -f deploy/service_account.yaml
    kubectl apply -f deploy/cluster_role.yaml
    kubectl apply -f deploy/cluster_role_binding.yaml
    kubectl apply -f deploy/k8s_gitlab_webhook_controller_secret.yaml
    kubectl apply -f deploy/k8s_gitlab_webhook_controller_deployment.yaml

deploy-chek:
    kubectl get pods -n kube-system

change-image image_name="" image_tag="v1.0.0":
    kubectl set image deployment/my-app my-app={{image_name}}:{{image_tag}}