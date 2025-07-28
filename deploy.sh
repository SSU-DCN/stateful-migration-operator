#!/bin/bash

# Deployment Script for Stateful Migration Operator Controllers
# This script deploys CheckpointBackup (member clusters via Karmada) and MigrationBackup (mgmt cluster) controllers

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_header() {
    echo -e "${PURPLE}[DEPLOY]${NC} $1"
}

print_step() {
    echo -e "${CYAN}[STEP]${NC} $1"
}

# Configuration
NAMESPACE="stateful-migration"
OPERATOR_NAMESPACE="stateful-migration"
DEFAULT_VERSION="v1.16"
DOCKERHUB_USERNAME="lehuannhatrang"
REPOSITORY_NAME="stateful-migration-operator"

# Default configurations
KARMADA_KUBECONFIG=""
MGMT_KUBECONFIG=""
DEPLOY_CHECKPOINT=false
DEPLOY_MIGRATION=false
VERSION="$DEFAULT_VERSION"
MEMBER_CLUSTERS=()
DRY_RUN=false

# Function to show usage
show_usage() {
    echo "Deployment Script for Stateful Migration Operator Controllers"
    echo "============================================================="
    echo
    echo "Usage: $0 [options]"
    echo
    echo "Options:"
    echo "  -c, --checkpoint              Deploy CheckpointBackup controller (DaemonSet to member clusters)"
    echo "  -m, --migration               Deploy MigrationBackup controller (management cluster)"
    echo "  -a, --all                     Deploy all controllers"
    echo "  -v, --version VERSION         Version tag for images (default: $DEFAULT_VERSION)"
    echo "  -k, --karmada-config PATH     Path to Karmada kubeconfig file"
    echo "  -g, --mgmt-config PATH        Path to management cluster kubeconfig file"
    echo "  -l, --clusters CLUSTER1,CLUSTER2  Comma-separated list of member cluster names"
    echo "  -d, --dry-run                 Show what would be deployed without actually deploying"
    echo "  -h, --help                    Show this help message"
    echo
    echo "Examples:"
    echo "  # Deploy all controllers"
    echo "  $0 --all --karmada-config ~/.kube/karmada --mgmt-config ~/.kube/config --clusters cluster1,cluster2"
    echo
    echo "  # Deploy only CheckpointBackup controller"
    echo "  $0 --checkpoint --karmada-config ~/.kube/karmada --clusters cluster1,cluster2"
    echo
    echo "  # Deploy only MigrationBackup controller"
    echo "  $0 --migration --mgmt-config ~/.kube/config"
    echo
    echo "  # Deploy with custom version"
    echo "  $0 --all --version v1.17 --karmada-config ~/.kube/karmada --mgmt-config ~/.kube/config --clusters cluster1,cluster2"
    echo
    echo "Prerequisites:"
    echo "  - Karmada control plane accessible"
    echo "  - Member clusters registered with Karmada"
    echo "  - kubectl installed and configured"
    echo "  - Registry credentials configured (for CheckpointBackup)"
}

# Parse command line arguments
parse_arguments() {
    while [[ $# -gt 0 ]]; do
        case $1 in
            -c|--checkpoint)
                DEPLOY_CHECKPOINT=true
                shift
                ;;
            -m|--migration)
                DEPLOY_MIGRATION=true
                shift
                ;;
            -a|--all)
                DEPLOY_CHECKPOINT=true
                DEPLOY_MIGRATION=true
                shift
                ;;
            -v|--version)
                VERSION="$2"
                shift 2
                ;;
            -k|--karmada-config)
                KARMADA_KUBECONFIG="$2"
                shift 2
                ;;
            -g|--mgmt-config)
                MGMT_KUBECONFIG="$2"
                shift 2
                ;;
            -l|--clusters)
                IFS=',' read -ra MEMBER_CLUSTERS <<< "$2"
                shift 2
                ;;
            -d|--dry-run)
                DRY_RUN=true
                shift
                ;;
            -h|--help)
                show_usage
                exit 0
                ;;
            *)
                print_error "Unknown option: $1"
                show_usage
                exit 1
                ;;
        esac
    done
}

# Validate prerequisites
validate_prerequisites() {
    print_step "Validating prerequisites..."
    
    # Check if at least one deployment type is selected
    if [[ "$DEPLOY_CHECKPOINT" == false && "$DEPLOY_MIGRATION" == false ]]; then
        print_error "No deployment type selected. Use --checkpoint, --migration, or --all"
        exit 1
    fi
    
    # Check kubectl
    if ! command -v kubectl &> /dev/null; then
        print_error "kubectl is not installed or not in PATH"
        exit 1
    fi
    
    # Validate CheckpointBackup prerequisites
    if [[ "$DEPLOY_CHECKPOINT" == true ]]; then
        if [[ -z "$KARMADA_KUBECONFIG" ]]; then
            print_error "Karmada kubeconfig is required for CheckpointBackup deployment (--karmada-config)"
            exit 1
        fi
        
        if [[ ! -f "$KARMADA_KUBECONFIG" ]]; then
            print_error "Karmada kubeconfig file not found: $KARMADA_KUBECONFIG"
            exit 1
        fi
        
        if [[ ${#MEMBER_CLUSTERS[@]} -eq 0 ]]; then
            print_error "Member clusters list is required for CheckpointBackup deployment (--clusters)"
            exit 1
        fi
        
        # Test Karmada connectivity
        if ! kubectl --kubeconfig="$KARMADA_KUBECONFIG" get clusters &>/dev/null; then
            print_error "Cannot connect to Karmada control plane"
            exit 1
        fi
    fi
    
    # Validate MigrationBackup prerequisites
    if [[ "$DEPLOY_MIGRATION" == true ]]; then
        if [[ -z "$MGMT_KUBECONFIG" ]]; then
            print_error "Management cluster kubeconfig is required for MigrationBackup deployment (--mgmt-config)"
            exit 1
        fi
        
        if [[ ! -f "$MGMT_KUBECONFIG" ]]; then
            print_error "Management cluster kubeconfig file not found: $MGMT_KUBECONFIG"
            exit 1
        fi
        
        # Test management cluster connectivity
        if ! kubectl --kubeconfig="$MGMT_KUBECONFIG" get nodes &>/dev/null; then
            print_error "Cannot connect to management cluster"
            exit 1
        fi
    fi
    
    print_success "Prerequisites validation passed"
}

# Execute kubectl command with dry-run support
execute_kubectl() {
    local kubeconfig="$1"
    shift
    local cmd="kubectl --kubeconfig=$kubeconfig $@"
    
    if [[ "$DRY_RUN" == true ]]; then
        echo "[DRY-RUN] $cmd"
        return 0
    else
        eval "$cmd"
    fi
}

# Deploy MigrationBackup controller to management cluster
deploy_migration_controller() {
    print_header "Deploying MigrationBackup Controller to Management Cluster"
    
    local image_name="${DOCKERHUB_USERNAME}/${REPOSITORY_NAME}:migrationBackup_${VERSION}"
    
    print_step "Checking cluster connectivity..."
    if ! execute_kubectl "$MGMT_KUBECONFIG" cluster-info >/dev/null 2>&1; then
        print_error "Cannot connect to management cluster"
        return 1
    fi
    print_success "Connected to cluster: $(execute_kubectl "$MGMT_KUBECONFIG" config current-context 2>/dev/null || echo "current context")"
    
    print_step "Checking if required CRDs exist..."
    local missing_crds=()
    
    if ! execute_kubectl "$MGMT_KUBECONFIG" get crd statefulmigrations.migration.dcnlab.com >/dev/null 2>&1; then
        missing_crds+=("statefulmigrations.migration.dcnlab.com")
    fi
    
    if ! execute_kubectl "$MGMT_KUBECONFIG" get crd checkpointbackups.migration.dcnlab.com >/dev/null 2>&1; then
        missing_crds+=("checkpointbackups.migration.dcnlab.com")
    fi
    
    if [[ ${#missing_crds[@]} -gt 0 ]]; then
        print_warning "Missing CRDs: ${missing_crds[*]}"
        print_step "Installing CRDs..."
        execute_kubectl "$MGMT_KUBECONFIG" apply -f config/crd/bases/
        print_success "CRDs installed from project"
    else
        print_success "All required CRDs found"
    fi
    
    print_step "Preparing deployment manifests..."
    
    # Create temporary deployment file from all-in-one template
    local temp_file="/tmp/migration-deployment-${RANDOM}.yaml"
    cp deploy/all-in-one.yaml "$temp_file"
    
    # Replace image placeholder with actual image
    if [[ "$DRY_RUN" == false ]]; then
        sed -i "s|YOUR_DOCKERHUB_USERNAME/stateful-migration-operator:latest|$image_name|g" "$temp_file"
    else
        echo "[DRY-RUN] Would replace image placeholder with: $image_name"
    fi
    
    print_success "Manifests prepared with image: $image_name"
    
    print_step "Applying deployment manifests..."
    if execute_kubectl "$MGMT_KUBECONFIG" apply -f "$temp_file"; then
        print_success "Manifests applied successfully"
    else
        print_error "Failed to apply manifests"
        if [[ "$DRY_RUN" == false ]]; then
            rm -f "$temp_file"
        fi
        return 1
    fi
    
    # Clean up temp file
    if [[ "$DRY_RUN" == false ]]; then
        rm -f "$temp_file"
    fi
    
    if [[ "$DRY_RUN" == false ]]; then
        print_step "Waiting for deployment to be ready..."
        if execute_kubectl "$MGMT_KUBECONFIG" wait --for=condition=Available deployment/migration-backup-controller -n "$OPERATOR_NAMESPACE" --timeout=300s; then
            print_success "Deployment is ready"
        else
            print_warning "Deployment may still be starting up"
            print_status "Check logs with: kubectl logs -n $OPERATOR_NAMESPACE deployment/migration-backup-controller"
        fi
    fi
    
    print_success "MigrationBackup controller deployed successfully"
}

# Deploy CheckpointBackup controller to member clusters via Karmada
deploy_checkpoint_controller() {
    print_header "Deploying CheckpointBackup Controller to Member Clusters via Karmada"
    
    local image_name="${DOCKERHUB_USERNAME}/${REPOSITORY_NAME}:checkpointBackup_${VERSION}"
    
    print_step "Creating stateful-migration namespace on Karmada..."
    execute_kubectl "$KARMADA_KUBECONFIG" create namespace "$NAMESPACE" --dry-run=client -o yaml | \
        execute_kubectl "$KARMADA_KUBECONFIG" apply -f -
    
    print_step "Applying CheckpointBackup CRD to Karmada..."
    execute_kubectl "$KARMADA_KUBECONFIG" apply -f config/crd/bases/migration.dcnlab.com_checkpointbackups.yaml
    
    print_step "Creating PropagationPolicy for namespace..."
    cat > /tmp/namespace-propagation.yaml <<EOF
apiVersion: policy.karmada.io/v1alpha1
kind: PropagationPolicy
metadata:
  name: stateful-migration-namespace
  namespace: $NAMESPACE
spec:
  resourceSelectors:
  - apiVersion: v1
    kind: Namespace
    name: $NAMESPACE
  placement:
    clusterAffinity:
      clusterNames:
$(printf '      - %s\n' "${MEMBER_CLUSTERS[@]}")
EOF
    
    execute_kubectl "$KARMADA_KUBECONFIG" apply -f /tmp/namespace-propagation.yaml
    rm -f /tmp/namespace-propagation.yaml
    
    print_step "Creating PropagationPolicy for CheckpointBackup CRD..."
    cat > /tmp/crd-propagation.yaml <<EOF
apiVersion: policy.karmada.io/v1alpha1
kind: PropagationPolicy
metadata:
  name: checkpointbackup-crd
  namespace: karmada-system
spec:
  resourceSelectors:
  - apiVersion: apiextensions.k8s.io/v1
    kind: CustomResourceDefinition
    name: checkpointbackups.migration.dcnlab.com
  placement:
    clusterAffinity:
      clusterNames:
$(printf '      - %s\n' "${MEMBER_CLUSTERS[@]}")
EOF
    
    execute_kubectl "$KARMADA_KUBECONFIG" apply -f /tmp/crd-propagation.yaml
    rm -f /tmp/crd-propagation.yaml
    
    print_step "Applying RBAC to Karmada..."
    execute_kubectl "$KARMADA_KUBECONFIG" apply -f config/rbac/checkpoint_backup_rbac.yaml
    
    print_step "Creating PropagationPolicy for RBAC..."
    cat > /tmp/rbac-propagation.yaml <<EOF
apiVersion: policy.karmada.io/v1alpha1
kind: PropagationPolicy
metadata:
  name: checkpoint-backup-rbac
  namespace: $NAMESPACE
spec:
  resourceSelectors:
  - apiVersion: v1
    kind: ServiceAccount
    name: checkpoint-backup-sa
  - apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRole
    name: checkpoint-backup-role
  - apiVersion: rbac.authorization.k8s.io/v1
    kind: ClusterRoleBinding
    name: checkpoint-backup-rolebinding
  placement:
    clusterAffinity:
      clusterNames:
$(printf '      - %s\n' "${MEMBER_CLUSTERS[@]}")
EOF
    
    execute_kubectl "$KARMADA_KUBECONFIG" apply -f /tmp/rbac-propagation.yaml
    rm -f /tmp/rbac-propagation.yaml
    
    print_step "Creating updated DaemonSet with correct image..."
    cat > /tmp/checkpoint-daemonset.yaml <<EOF
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: checkpoint-backup-controller
  namespace: $NAMESPACE
  labels:
    app.kubernetes.io/name: checkpoint-backup-controller
    app.kubernetes.io/part-of: stateful-migration-operator
    app.kubernetes.io/component: controller
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: checkpoint-backup-controller
  template:
    metadata:
      labels:
        app.kubernetes.io/name: checkpoint-backup-controller
        app.kubernetes.io/part-of: stateful-migration-operator
        app.kubernetes.io/component: controller
    spec:
      serviceAccountName: checkpoint-backup-sa
      hostNetwork: true
      hostPID: true
      securityContext:
        runAsUser: 0
        runAsGroup: 0
        fsGroup: 0
      tolerations:
      - operator: Exists
        effect: NoSchedule
      - operator: Exists
        effect: NoExecute
      containers:
      - name: controller
        image: $image_name
        imagePullPolicy: Always
        command:
        - /manager
        args:
        - --zap-log-level=info
        - --zap-encoder=console
        - --enable-checkpoint-backup-controller=true
        - --enable-migration-backup-controller=false
        - --enable-migration-restore-controller=false
        env:
        - name: NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        - name: NODE_IP
          valueFrom:
            fieldRef:
              fieldPath: status.hostIP
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
        ports:
        - containerPort: 8080
          name: metrics
          protocol: TCP
        - containerPort: 9443
          name: webhook-server
          protocol: TCP
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8081
          initialDelaySeconds: 15
          periodSeconds: 20
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8081
          initialDelaySeconds: 5
          periodSeconds: 10
        resources:
          limits:
            cpu: 500m
            memory: 512Mi
          requests:
            cpu: 100m
            memory: 128Mi
        securityContext:
          privileged: true
          capabilities:
            add:
            - SYS_ADMIN
            - SYS_PTRACE
        volumeMounts:
        - name: kubelet-checkpoints
          mountPath: /var/lib/kubelet/checkpoints
          readOnly: false
        - name: buildah-storage
          mountPath: /var/lib/containers
          readOnly: false
        - name: buildah-user-storage
          mountPath: /home/nonroot/.local/share/containers
          readOnly: false
        - name: etc-containers
          mountPath: /etc/containers
          readOnly: true
        - name: run-containers
          mountPath: /run/containers
          readOnly: false
        - name: var-run
          mountPath: /var/run
          readOnly: false
      volumes:
      - name: kubelet-checkpoints
        hostPath:
          path: /var/lib/kubelet/checkpoints
          type: DirectoryOrCreate
      - name: buildah-storage
        hostPath:
          path: /var/lib/containers
          type: DirectoryOrCreate
      - name: buildah-user-storage
        hostPath:
          path: /var/lib/buildah-user-storage
          type: DirectoryOrCreate
      - name: etc-containers
        hostPath:
          path: /etc/containers
          type: DirectoryOrCreate
      - name: run-containers
        hostPath:
          path: /run/containers
          type: DirectoryOrCreate
      - name: var-run
        hostPath:
          path: /var/run
          type: Directory
      terminationGracePeriodSeconds: 30
EOF
    
    execute_kubectl "$KARMADA_KUBECONFIG" apply -f /tmp/checkpoint-daemonset.yaml
    rm -f /tmp/checkpoint-daemonset.yaml
    
    print_step "Creating PropagationPolicy for DaemonSet..."
    cat > /tmp/daemonset-propagation.yaml <<EOF
apiVersion: policy.karmada.io/v1alpha1
kind: PropagationPolicy
metadata:
  name: checkpoint-backup-daemonset
  namespace: $NAMESPACE
spec:
  resourceSelectors:
  - apiVersion: apps/v1
    kind: DaemonSet
    name: checkpoint-backup-controller
  placement:
    clusterAffinity:
      clusterNames:
$(printf '      - %s\n' "${MEMBER_CLUSTERS[@]}")
EOF
    
    execute_kubectl "$KARMADA_KUBECONFIG" apply -f /tmp/daemonset-propagation.yaml
    rm -f /tmp/daemonset-propagation.yaml
    
    print_success "CheckpointBackup controller deployed successfully"
}

# Show deployment status
show_deployment_status() {
    print_header "Deployment Status"
    
    if [[ "$DEPLOY_MIGRATION" == true && "$DRY_RUN" == false ]]; then
        print_step "Checking MigrationBackup controller status..."
        execute_kubectl "$MGMT_KUBECONFIG" get deployment migration-backup-controller -n "$OPERATOR_NAMESPACE" -o wide || true
        execute_kubectl "$MGMT_KUBECONFIG" get pods -n "$OPERATOR_NAMESPACE" -l app.kubernetes.io/name=migration-backup-controller || true
        execute_kubectl "$MGMT_KUBECONFIG" get svc -n "$OPERATOR_NAMESPACE" migration-backup-controller-metrics || true
    fi
    
    if [[ "$DEPLOY_CHECKPOINT" == true && "$DRY_RUN" == false ]]; then
        print_step "Checking CheckpointBackup controller propagation..."
        execute_kubectl "$KARMADA_KUBECONFIG" get propagationpolicy -n "$NAMESPACE" || true
        
        print_step "Checking DaemonSet on member clusters..."
        for cluster in "${MEMBER_CLUSTERS[@]}"; do
            echo "Cluster: $cluster"
            execute_kubectl "$KARMADA_KUBECONFIG" get daemonset checkpoint-backup-controller -n "$NAMESPACE" --context="$cluster" -o wide || true
        done
    fi
}

# Show next steps
show_next_steps() {
    print_header "Next Steps"
    
    if [[ "$DEPLOY_CHECKPOINT" == true ]]; then
        echo
        print_status "For CheckpointBackup controller:"
        echo "1. Ensure registry credentials are configured:"
        echo "   kubectl --kubeconfig=$KARMADA_KUBECONFIG apply -f config/checkpoint-backup/registry-credentials-secret.yaml"
        echo
        echo "2. Update registry credentials with actual values"
        echo
        echo "3. Create PropagationPolicy for registry credentials to member clusters"
        echo
        echo "4. Verify DaemonSet is running on member clusters:"
        echo "   kubectl get pods -n $NAMESPACE -l app.kubernetes.io/name=checkpoint-backup-controller"
    fi
    
    if [[ "$DEPLOY_MIGRATION" == true ]]; then
        echo
        print_status "For MigrationBackup controller:"
        echo "1. Verify controller is running:"
        echo "   kubectl --kubeconfig=$MGMT_KUBECONFIG get pods -n $OPERATOR_NAMESPACE"
        echo
        echo "2. Check controller logs:"
        echo "   kubectl --kubeconfig=$MGMT_KUBECONFIG logs -n $OPERATOR_NAMESPACE deployment/migration-backup-controller -f"
        echo
        echo "3. Check StatefulMigrations:"
        echo "   kubectl --kubeconfig=$MGMT_KUBECONFIG get statefulmigrations -A"
        echo
        echo "4. Check CheckpointBackups:"
        echo "   kubectl --kubeconfig=$MGMT_KUBECONFIG get checkpointbackups -A"
        echo
        echo "5. Port forward metrics (optional):"
        echo "   kubectl --kubeconfig=$MGMT_KUBECONFIG port-forward -n $OPERATOR_NAMESPACE svc/migration-backup-controller-metrics 8080:8080"
        echo
        echo "6. Create StatefulMigration resources to trigger migrations"
    fi
    
    echo
    print_status "Documentation:"
    echo "- CheckpointBackup setup: config/checkpoint-backup/README.md"
    echo "- Buildah integration: BUILDAH_INTEGRATION.md"
    echo "- Build process: BUILD_USAGE.md"
}

# Main execution
main() {
    # Show header
    echo "🚀 Stateful Migration Operator Deployment Script"
    echo "================================================="
    echo "Version: $VERSION"
    echo
    
    # Parse arguments
    parse_arguments "$@"
    
    # Show configuration
    echo "Deployment Configuration:"
    echo "  CheckpointBackup Controller: $([ "$DEPLOY_CHECKPOINT" == true ] && echo "✅ Yes" || echo "❌ No")"
    echo "  MigrationBackup Controller:  $([ "$DEPLOY_MIGRATION" == true ] && echo "✅ Yes" || echo "❌ No")"
    echo "  Version: $VERSION"
    echo "  Dry Run: $([ "$DRY_RUN" == true ] && echo "✅ Yes" || echo "❌ No")"
    if [[ "$DEPLOY_CHECKPOINT" == true ]]; then
        echo "  Member Clusters: ${MEMBER_CLUSTERS[*]}"
        echo "  Karmada Config: $KARMADA_KUBECONFIG"
    fi
    if [[ "$DEPLOY_MIGRATION" == true ]]; then
        echo "  Management Config: $MGMT_KUBECONFIG"
    fi
    echo
    
    # Validate prerequisites
    validate_prerequisites
    
    # Deploy controllers
    if [[ "$DEPLOY_MIGRATION" == true ]]; then
        deploy_migration_controller
        echo
    fi
    
    if [[ "$DEPLOY_CHECKPOINT" == true ]]; then
        deploy_checkpoint_controller
        echo
    fi
    
    # Show status and next steps
    if [[ "$DRY_RUN" == false ]]; then
        show_deployment_status
        echo
    fi
    
    show_next_steps
    
    print_success "Deployment completed successfully!"
}

# Show usage if help is requested
if [[ "$1" == "-h" || "$1" == "--help" ]]; then
    show_usage
    exit 0
fi

# Run main function
main "$@" 