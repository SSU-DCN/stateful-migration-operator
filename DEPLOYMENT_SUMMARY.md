# Deployment Script Summary

## 🚀 **deploy.sh** - Automated Deployment for Stateful Migration Operator

### What It Does
The `deploy.sh` script provides **one-command deployment** for both controllers:

1. **CheckpointBackup Controller** → DaemonSet on member clusters (via Karmada)
2. **MigrationBackup Controller** → Deployment on management cluster

### Quick Start Examples

#### Deploy Everything
```bash
./deploy.sh --all \
  --karmada-config ~/.kube/karmada \
  --mgmt-config ~/.kube/config \
  --clusters cluster1,cluster2 \
  --version v1.17
```

#### Deploy Only CheckpointBackup (DaemonSet)
```bash
./deploy.sh --checkpoint \
  --karmada-config ~/.kube/karmada \
  --clusters cluster1,cluster2
# Will prompt for registry credentials interactively

# Or provide credentials via flags:
./deploy.sh --checkpoint \
  --karmada-config ~/.kube/karmada \
  --clusters cluster1,cluster2 \
  --registry-username myuser \
  --registry-url myregistry.com
# Will prompt for password only
```

#### Deploy Only MigrationBackup (Management)
```bash
./deploy.sh --migration \
  --mgmt-config ~/.kube/config
```

#### Preview Changes (Dry Run)
```bash
./deploy.sh --all --dry-run \
  --karmada-config ~/.kube/karmada \
  --mgmt-config ~/.kube/config \
  --clusters cluster1,cluster2
```

### What Gets Automatically Deployed

#### ✅ **CheckpointBackup Controller (via Karmada)**
- ✅ Namespace: `stateful-migration`
- ✅ CRD: `checkpointbackups.migration.dcnlab.com`
- ✅ RBAC: Service account + cluster permissions
- ✅ Registry Credentials: Automatically created and propagated
- ✅ DaemonSet: Buildah-enabled controller
- ✅ PropagationPolicies: Distributes to member clusters
- ✅ Image: `lehuannhatrang/stateful-migration-operator:checkpointBackup_<VERSION>`

#### ✅ **MigrationBackup Controller (Management Cluster)**
- ✅ Namespace: `stateful-migration`
- ✅ CRDs: All migration CRDs
- ✅ RBAC: Service account and cluster permissions (follows deploy/all-in-one.yaml)
- ✅ Deployment: Management controller with metrics service
- ✅ Image: `lehuannhatrang/stateful-migration-operator:migrationBackup_<VERSION>`

### Features
- 🎯 **Selective Deployment**: Choose which controllers to deploy
- 🔍 **Dry Run Mode**: Preview changes before applying
- 📝 **Version Control**: Specify image versions
- 🔐 **Automatic Registry Setup**: Interactive credential prompts and secret creation
- 🛡️ **Validation**: Checks prerequisites and connectivity
- 🎨 **Colored Output**: Clear status and progress indicators
- 📊 **Status Reporting**: Shows deployment results

### Prerequisites
- kubectl installed
- Karmada control plane access
- Member clusters registered with Karmada
- Management cluster access
- Built and pushed controller images

### Post-Deployment
The script provides **next steps guidance** for:
- Registry credentials configuration
- Verification commands
- Troubleshooting tips
- Test resource creation

### Files Created
- `deploy.sh` - Main deployment script
- `DEPLOYMENT_GUIDE.md` - Comprehensive deployment guide
- `DEPLOYMENT_SUMMARY.md` - This quick reference

### Ready to Use! 🎉
```bash
chmod +x deploy.sh
./deploy.sh --help
```

This automated deployment solution makes it easy to deploy the Stateful Migration Operator across complex Karmada multi-cluster environments! 