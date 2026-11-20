# Kubernetes Deployment

This directory contains Kubernetes manifests for deploying KoreCube in a Kubernetes cluster.

## Quick Start

```bash
# Apply all manifests
kubectl apply -f deployments/kubernetes/

# Check deployment status
kubectl get pods -l app=korecube

# Access the application
kubectl port-forward service/korecube-frontend 8080:80
```

## Prerequisites

- Kubernetes cluster v1.20+
- kubectl configured to access your cluster
- StorageClass available for persistent volumes
- LoadBalancer or Ingress controller (for external access)

## Architecture

```
┌─────────────────┐    ┌─────────────────┐
│   Frontend      │    │   Backend       │
│   (korecube-ui) │────│   (korecube)    │
└─────────────────┘    └─────────────────┘
         │                       │
         │              ┌─────────────────┐
         │              │   MySQL         │
         │              │   (optional)    │
         │              └─────────────────┘
         │
┌─────────────────┐    ┌─────────────────┐
│   Prometheus    │    │   Grafana       │
│   (monitoring)  │────│   (dashboard)   │
└─────────────────┘    └─────────────────┘
```

## Manifests

| File | Description |
|------|-------------|
| `namespace.yaml` | Namespace for KoreCube resources |
| `configmap.yaml` | Configuration files |
| `secret.yaml` | Sensitive configuration data |
| `deployment-backend.yaml` | Backend application deployment |
| `deployment-frontend.yaml` | Frontend application deployment |
| `service-backend.yaml` | Backend service |
| `service-frontend.yaml` | Frontend service |
| `ingress.yaml` | Ingress for external access |
| `pvc.yaml` | Persistent volume claims |
| `rbac.yaml` | RBAC permissions |

## Configuration

### Environment Variables

The backend deployment supports the following environment variables:

```yaml
env:
- name: GIN_MODE
  value: "release"
- name: KORECUBE_CONFIG_PATH
  value: "/app/configs/config.yaml"
- name: KUBECONFIG
  value: "/etc/kubeconfig/config"
```

### Secrets

Create secrets for sensitive data:

```bash
# JWT secret
kubectl create secret generic korecube-jwt \
  --from-literal=secret=your-jwt-secret

# Database credentials (if using external database)
kubectl create secret generic korecube-db \
  --from-literal=username=korecube \
  --from-literal=password=your-password
```

### ConfigMap

Configuration files are mounted via ConfigMap:

```bash
# Create ConfigMap from local config
kubectl create configmap korecube-config \
  --from-file=configs/config.yaml
```

## Storage

### Persistent Volumes

The deployment uses PersistentVolumeClaims for:
- Configuration storage
- Log storage
- Database storage (if using MySQL)

### StorageClass

Ensure you have a default StorageClass or specify one:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: korecube-config
spec:
  storageClassName: fast-ssd  # Specify your StorageClass
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
```

## Networking

### Services

- **Backend Service**: ClusterIP on port 8080
- **Frontend Service**: ClusterIP on port 80
- **Database Service**: ClusterIP on port 3306 (if enabled)

### Ingress

Configure ingress for external access:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: korecube-ingress
  annotations:
    kubernetes.io/ingress.class: nginx
    cert-manager.io/cluster-issuer: letsencrypt-prod
spec:
  tls:
  - hosts:
    - korecube.example.com
    secretName: korecube-tls
  rules:
  - host: korecube.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: korecube-frontend
            port:
              number: 80
      - path: /api
        pathType: Prefix
        backend:
          service:
            name: korecube-backend
            port:
              number: 8080
```

## RBAC

KoreCube requires specific Kubernetes permissions:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: korecube-role
rules:
- apiGroups: [""]
  resources: ["pods", "services", "configmaps", "secrets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: ["apps"]
  resources: ["deployments", "replicasets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
```

## Monitoring

### Prometheus

Deploy Prometheus for metrics collection:

```bash
# Apply Prometheus manifests
kubectl apply -f monitoring/prometheus/
```

### Grafana

Deploy Grafana for visualization:

```bash
# Apply Grafana manifests
kubectl apply -f monitoring/grafana/
```

## Scaling

### Horizontal Pod Autoscaler

Enable auto-scaling based on CPU/memory:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: korecube-backend-hpa
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: korecube-backend
  minReplicas: 2
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
```

### Vertical Pod Autoscaler

Enable VPA for automatic resource adjustment:

```yaml
apiVersion: autoscaling.k8s.io/v1
kind: VerticalPodAutoscaler
metadata:
  name: korecube-backend-vpa
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: korecube-backend
  updatePolicy:
    updateMode: "Auto"
```

## Security

### Pod Security Standards

Apply pod security standards:

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: korecube
  labels:
    pod-security.kubernetes.io/enforce: restricted
    pod-security.kubernetes.io/audit: restricted
    pod-security.kubernetes.io/warn: restricted
```

### Network Policies

Implement network segmentation:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: korecube-network-policy
spec:
  podSelector:
    matchLabels:
      app: korecube
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - podSelector:
        matchLabels:
          app: korecube-frontend
    ports:
    - protocol: TCP
      port: 8080
```

## Troubleshooting

### Common Issues

1. **Pod not starting**
   ```bash
   kubectl describe pod <pod-name>
   kubectl logs <pod-name>
   ```

2. **Service not accessible**
   ```bash
   kubectl get svc
   kubectl describe svc korecube-backend
   ```

3. **Ingress not working**
   ```bash
   kubectl get ingress
   kubectl describe ingress korecube-ingress
   ```

### Debug Commands

```bash
# Check all resources
kubectl get all -l app=korecube

# Check events
kubectl get events --sort-by=.metadata.creationTimestamp

# Check resource usage
kubectl top pods
kubectl top nodes

# Port forward for debugging
kubectl port-forward deployment/korecube-backend 8080:8080
```

## Cleanup

```bash
# Delete all KoreCube resources
kubectl delete -f deployments/kubernetes/

# Or delete by label
kubectl delete all -l app=korecube
```