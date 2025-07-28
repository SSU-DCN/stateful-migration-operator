# Deployment Guide for Stateful Migration Operator

This guide explains how to deploy both CheckpointBackup and MigrationBackup controllers using the automated `deploy.sh` script.

## Overview

The deployment script (`deploy.sh`) handles:
- **CheckpointBackup Controller**: Deployed as DaemonSet to member clusters via Karmada PropagationPolicies
- **MigrationBackup Controller**: Deployed to management/control plane cluster
- **Automatic RBAC**: Creates necessary service accounts, roles, and bindings
- **Namespace Management**: Creates and propagates namespaces
- **CRD Propagation**: Ensures CRDs are available on member clusters

## Prerequisites

### 1. System Requirements
- `kubectl` installed and configured
- Access to Karmada control plane
- Access to management cluster
- Docker images built and pushed to registry

### 2. Karmada Setup
- Karmada control plane running and accessible
- Member clusters registered with Karmada
- Kubeconfig file for Karmada control plane

### 3. Management Cluster
- Kubernetes cluster for running MigrationBackup controller
- Kubeconfig file for management cluster

### 4. Registry Credentials (for CheckpointBackup)
- Container registry access
- Registry credentials secret configured

## Usage

### Basic Syntax
```bash
./deploy.sh [options]
```

### Options
| Option | Description | Required |
|--------|-------------|----------|
| `-c, --checkpoint` | Deploy CheckpointBackup controller | Choice |
| `-m, --migration` | Deploy MigrationBackup controller | Choice |
| `-a, --all` | Deploy all controllers | Choice |
| `-v, --version VERSION` | Version tag for images (default: v1.16) | No |
| `-k, --karmada-config PATH` | Path to Karmada kubeconfig | For checkpoint |
| `-g, --mgmt-config PATH` | Path to management cluster kubeconfig | For migration |
| `-l, --clusters LIST` | Comma-separated member cluster names | For checkpoint |
| `-d, --dry-run` | Show what would be deployed | No |
| `-h, --help` | Show help message | No |

## Deployment Scenarios

### 1. Deploy All Controllers
```bash
./deploy.sh --all \
  --karmada-config ~/.kube/karmada \
  --mgmt-config ~/.kube/config \
  --clusters cluster1,cluster2,cluster3 \
  --version v1.17
```

### 2. Deploy Only CheckpointBackup Controller
```bash
./deploy.sh --checkpoint \
  --karmada-config ~/.kube/karmada \
  --clusters cluster1,cluster2 \
  --version v1.17
```

### 3. Deploy Only MigrationBackup Controller
```bash
./deploy.sh --migration \
  --mgmt-config ~/.kube/config \
  --version v1.17
```

### 4. Dry Run (Preview Changes)
```bash
./deploy.sh --all \
  --karmada-config ~/.kube/karmada \
  --mgmt-config ~/.kube/config \
  --clusters cluster1,cluster2 \
  --dry-run
```

## What Gets Deployed

### CheckpointBackup Controller (Member Clusters)

#### 🏗️ **Resources Created on Karmada**
1. **Namespace**: `stateful-migration`
2. **CRD**: `checkpointbackups.migration.dcnlab.com`
3. **RBAC**: Service account, ClusterRole, ClusterRoleBinding
4. **DaemonSet**: CheckpointBackup controller with buildah
5. **PropagationPolicies**: For namespace, CRD, RBAC, DaemonSet

#### 📦 **Container Image**
- `lehuannhatrang/stateful-migration-operator:checkpointBackup_<VERSION>`
- Includes buildah and container tools
- Size: ~120MB

#### 🔧 **Configuration**
- Privileged container with `SYS_ADMIN`, `SYS_PTRACE` capabilities
- Host network and PID access
- Volume mounts for kubelet checkpoints and buildah storage

### MigrationBackup Controller (Management Cluster)

#### 🏗️ **Resources Created**
1. **Namespace**: `stateful-migration-operator-system`
2. **CRDs**: All migration-related CRDs
3. **RBAC**: Controller manager service account and permissions
4. **Deployment**: MigrationBackup controller

#### 📦 **Container Image**
- `lehuannhatrang/stateful-migration-operator:migrationBackup_<VERSION>`
- Minimal distroless image
- Size: ~15MB

#### 🔧 **Configuration**
- Non-privileged container
- Leader election enabled
- Metrics and health endpoints

## Post-Deployment Steps

### 1. Verify CheckpointBackup Controller
```bash
# Check PropagationPolicies
kubectl --kubeconfig ~/.kube/karmada get propagationpolicy -n stateful-migration

# Check DaemonSet on member clusters
kubectl get daemonset checkpoint-backup-controller -n stateful-migration

# Check pods on member clusters
kubectl get pods -n stateful-migration -l app.kubernetes.io/name=checkpoint-backup-controller
```

### 2. Configure Registry Credentials
```bash
# Apply registry credentials template (update with real credentials first)
kubectl --kubeconfig ~/.kube/karmada apply -f config/checkpoint-backup/registry-credentials-secret.yaml

# Create PropagationPolicy for registry credentials
kubectl --kubeconfig ~/.kube/karmada apply -f - <<EOF
apiVersion: policy.karmada.io/v1alpha1
kind: PropagationPolicy
metadata:
  name: registry-credentials-propagation
  namespace: stateful-migration
spec:
  resourceSelectors:
  - apiVersion: v1
    kind: Secret
    name: registry-credentials
  placement:
    clusterAffinity:
      clusterNames:
      - cluster1
      - cluster2
EOF
```

### 3. Verify MigrationBackup Controller
```bash
# Check deployment
kubectl --kubeconfig ~/.kube/config get deployment migration-backup-controller -n stateful-migration-operator-system

# Check pods
kubectl --kubeconfig ~/.kube/config get pods -n stateful-migration-operator-system

# Check logs
kubectl --kubeconfig ~/.kube/config logs -n stateful-migration-operator-system -l control-plane=controller-manager
```

### 4. Test the Setup
```bash
# Create a test StatefulMigration resource
kubectl --kubeconfig ~/.kube/config apply -f - <<EOF
apiVersion: migration.dcnlab.com/v1
kind: StatefulMigration
metadata:
  name: test-migration
  namespace: default
spec:
  resourceRef:
    kind: StatefulSet
    name: my-statefulset
    namespace: default
  schedule: "0 2 * * *"
  sourceClusters:
  - cluster1
  registry:
    server: "your-registry.com"
    repository: "your-repo/checkpoints"
EOF
```

## Troubleshooting

### Common Issues

#### 1. **PropagationPolicy Not Working**
```bash
# Check PropagationPolicy status
kubectl --kubeconfig ~/.kube/karmada get propagationpolicy -n stateful-migration -o wide

# Check cluster registration
kubectl --kubeconfig ~/.kube/karmada get clusters
```

#### 2. **DaemonSet Not Scheduling**
```bash
# Check node selectors and tolerations
kubectl describe daemonset checkpoint-backup-controller -n stateful-migration

# Check node conditions
kubectl get nodes -o wide
```

#### 3. **Controller Not Starting**
```bash
# Check pod logs
kubectl logs -n stateful-migration -l app.kubernetes.io/name=checkpoint-backup-controller

# Check events
kubectl get events -n stateful-migration --sort-by='.lastTimestamp'
```

#### 4. **Registry Issues**
```bash
# Test registry connectivity from pod
kubectl exec -n stateful-migration <pod-name> -- buildah login your-registry.com

# Check secret propagation
kubectl get secret registry-credentials -n stateful-migration
```

### Debug Commands

```bash
# Check buildah functionality
kubectl exec -n stateful-migration <pod-name> -- buildah version

# Check storage configuration
kubectl exec -n stateful-migration <pod-name> -- buildah info

# Check kubelet checkpoint API
kubectl exec -n stateful-migration <pod-name> -- curl -k https://localhost:10250/healthz
```

## Uninstallation

### Remove CheckpointBackup Controller
```bash
# Delete PropagationPolicies
kubectl --kubeconfig ~/.kube/karmada delete propagationpolicy -n stateful-migration --all

# Delete DaemonSet
kubectl --kubeconfig ~/.kube/karmada delete daemonset checkpoint-backup-controller -n stateful-migration

# Delete RBAC
kubectl --kubeconfig ~/.kube/karmada delete -f config/rbac/checkpoint_backup_rbac.yaml

# Delete namespace
kubectl --kubeconfig ~/.kube/karmada delete namespace stateful-migration
```

### Remove MigrationBackup Controller
```bash
# Delete deployment
kubectl --kubeconfig ~/.kube/config delete deployment migration-backup-controller -n stateful-migration-operator-system

# Delete RBAC
kubectl --kubeconfig ~/.kube/config delete -f config/rbac/

# Delete CRDs
kubectl --kubeconfig ~/.kube/config delete -f config/crd/bases/

# Delete namespace
kubectl --kubeconfig ~/.kube/config delete namespace stateful-migration-operator-system
```

## Advanced Configuration

### Custom Namespaces
Edit the script variables:
```bash
NAMESPACE="custom-migration-namespace"
OPERATOR_NAMESPACE="custom-operator-namespace"
```

### Custom Image Registry
Edit the script variables:
```bash
DOCKERHUB_USERNAME="your-registry.com/your-org"
REPOSITORY_NAME="custom-operator"
```

### Additional Member Clusters
Add clusters to the list:
```bash
./deploy.sh --checkpoint \
  --clusters cluster1,cluster2,cluster3,cluster4 \
  --karmada-config ~/.kube/karmada
```

## Performance Considerations

### Resource Requirements
- **CheckpointBackup Controller**: 100m CPU, 128Mi memory (requests), 500m CPU, 512Mi memory (limits)
- **MigrationBackup Controller**: 10m CPU, 64Mi memory (requests), 500m CPU, 128Mi memory (limits)

### Storage Requirements
- **Kubelet checkpoints**: 50MB-500MB per container
- **Buildah storage**: 1GB-10GB per node
- **Registry bandwidth**: Consider checkpoint image sizes

### Scaling
- CheckpointBackup controllers scale with cluster nodes (DaemonSet)
- MigrationBackup controller typically runs as single replica with leader election

This deployment script provides a complete, production-ready solution for deploying the Stateful Migration Operator across your Karmada-managed clusters! 🚀 