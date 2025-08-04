/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	karmadapolicyv1alpha1 "github.com/karmada-io/karmada/pkg/apis/policy/v1alpha1"
	karmadaworkv1alpha1 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha1"
	karmadaworkv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	migrationv1 "github.com/lehuannhatrang/stateful-migration-operator/api/v1"
)

const (
	// RestoreCheckInterval is the interval at which the controller checks for ResourceBinding changes
	RestoreCheckInterval = 30 * time.Second
)

// MigrationRestoreReconciler reconciles a StatefulMigration object for restore operations
type MigrationRestoreReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	KarmadaClient *KarmadaClient
}

// +kubebuilder:rbac:groups=migration.dcnlab.com,resources=statefulmigrations,verbs=get;list;watch
// +kubebuilder:rbac:groups=migration.dcnlab.com,resources=statefulmigrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=migration.dcnlab.com,resources=checkpointbackups,verbs=get;list;watch
// +kubebuilder:rbac:groups=migration.dcnlab.com,resources=checkpointrestores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=work.karmada.io,resources=resourcebindings,verbs=get;list;watch
// +kubebuilder:rbac:groups=work.karmada.io,resources=works,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=policy.karmada.io,resources=propagationpolicies,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *MigrationRestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the StatefulMigration instance
	var statefulMigration migrationv1.StatefulMigration
	if err := r.Get(ctx, req.NamespacedName, &statefulMigration); err != nil {
		log.Error(err, "unable to fetch StatefulMigration")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("MigrationRestore controller received StatefulMigration",
		"name", statefulMigration.Name,
		"namespace", statefulMigration.Namespace,
		"resourceRef", statefulMigration.Spec.ResourceRef,
		"sourceClusters", statefulMigration.Spec.SourceClusters)

	// Skip if Karmada client is not available
	if r.KarmadaClient == nil {
		log.Info("Skipping restore logic - Karmada client not available, will retry")
		return ctrl.Result{RequeueAfter: RestoreCheckInterval}, nil
	}

	// Process each source cluster
	for _, sourceCluster := range statefulMigration.Spec.SourceClusters {
		if err := r.processSourceCluster(ctx, &statefulMigration, sourceCluster); err != nil {
			log.Error(err, "failed to process source cluster", "cluster", sourceCluster)
			return ctrl.Result{}, err
		}
	}

	// Requeue periodically to check for ResourceBinding changes
	// Since ResourceBinding resources exist in Karmada control plane and we can't watch them
	// from the management cluster, we need to poll periodically
	return ctrl.Result{RequeueAfter: RestoreCheckInterval}, nil
}

// processSourceCluster processes a single source cluster for restore operations
func (r *MigrationRestoreReconciler) processSourceCluster(ctx context.Context, statefulMigration *migrationv1.StatefulMigration, sourceCluster string) error {
	log := log.FromContext(ctx)

	// Find the resource binding for this resource and cluster
	resourceBinding, err := r.findResourceBinding(ctx, statefulMigration.Spec.ResourceRef, sourceCluster)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("Resource binding not found, skipping", "cluster", sourceCluster, "resource", statefulMigration.Spec.ResourceRef.Name)
			return nil
		}
		return fmt.Errorf("failed to find resource binding: %w", err)
	}

	// Check if the source cluster is still in the clusterAffinity
	if r.isSourceClusterStillAvailable(resourceBinding, sourceCluster) {
		log.Info("Source cluster is still available, no restore needed",
			"cluster", sourceCluster,
			"clusterAffinity", resourceBinding.Spec.Clusters)
		return nil
	}

	log.Info("Source cluster is no longer available, starting restore process",
		"cluster", sourceCluster,
		"clusterAffinity", resourceBinding.Spec.Clusters)

	// Check if there are checkpoint backups for this resource
	checkpointBackups, err := r.findCheckpointBackups(ctx, statefulMigration.Spec.ResourceRef, sourceCluster)
	if err != nil {
		return fmt.Errorf("failed to find checkpoint backups: %w", err)
	}

	if len(checkpointBackups) == 0 {
		log.Info("No checkpoint backups found for resource",
			"resource", statefulMigration.Spec.ResourceRef.Name,
			"cluster", sourceCluster)
		return nil
	}

	// Start restore process
	return r.startRestoreProcess(ctx, statefulMigration, sourceCluster, checkpointBackups)
}

// findResourceBinding finds the resource binding for a specific resource and cluster
func (r *MigrationRestoreReconciler) findResourceBinding(ctx context.Context, resourceRef migrationv1.ResourceRef, sourceCluster string) (*karmadaworkv1alpha2.ResourceBinding, error) {
	log := log.FromContext(ctx)

	// List all resource bindings
	var resourceBindings karmadaworkv1alpha2.ResourceBindingList
	if err := r.KarmadaClient.List(ctx, &resourceBindings); err != nil {
		return nil, fmt.Errorf("failed to list resource bindings: %w", err)
	}

	// Find the binding that matches our resource and cluster
	for _, binding := range resourceBindings.Items {
		// Check if this binding is for our resource
		if binding.Spec.Resource.APIVersion == resourceRef.APIVersion &&
			binding.Spec.Resource.Kind == resourceRef.Kind &&
			binding.Spec.Resource.Name == resourceRef.Name &&
			binding.Spec.Resource.Namespace == resourceRef.Namespace {

			// Check if this binding includes our source cluster
			for _, cluster := range binding.Spec.Clusters {
				if cluster.Name == sourceCluster {
					log.Info("Found resource binding",
						"binding", binding.Name,
						"resource", resourceRef.Name,
						"cluster", sourceCluster)
					return &binding, nil
				}
			}
		}
	}

	return nil, errors.NewNotFound(schema.GroupResource{Group: "work.karmada.io", Resource: "resourcebindings"}, "not found")
}

// isSourceClusterStillAvailable checks if the source cluster is still in the clusterAffinity
func (r *MigrationRestoreReconciler) isSourceClusterStillAvailable(binding *karmadaworkv1alpha2.ResourceBinding, sourceCluster string) bool {
	for _, cluster := range binding.Spec.Clusters {
		if cluster.Name == sourceCluster {
			return true
		}
	}
	return false
}

// findCheckpointBackups finds checkpoint backups for a specific resource and cluster
func (r *MigrationRestoreReconciler) findCheckpointBackups(ctx context.Context, resourceRef migrationv1.ResourceRef, sourceCluster string) ([]migrationv1.CheckpointBackup, error) {
	log := log.FromContext(ctx)

	var checkpointBackups migrationv1.CheckpointBackupList
	if err := r.KarmadaClient.List(ctx, &checkpointBackups); err != nil {
		return nil, fmt.Errorf("failed to list checkpoint backups: %w", err)
	}

	var matchingBackups []migrationv1.CheckpointBackup
	for _, backup := range checkpointBackups.Items {
		// Check if this backup is for our resource
		if backup.Spec.ResourceRef.APIVersion == resourceRef.APIVersion &&
			backup.Spec.ResourceRef.Kind == resourceRef.Kind &&
			backup.Spec.ResourceRef.Name == resourceRef.Name &&
			backup.Spec.ResourceRef.Namespace == resourceRef.Namespace {

			// Check if this backup is from our source cluster
			// We can identify this by checking the pod namespace/name pattern or labels
			if backup.Spec.PodRef.Namespace == resourceRef.Namespace {
				matchingBackups = append(matchingBackups, backup)
			}
		}
	}

	log.Info("Found checkpoint backups",
		"resource", resourceRef.Name,
		"cluster", sourceCluster,
		"count", len(matchingBackups))

	return matchingBackups, nil
}

// startRestoreProcess starts the restore process for the given resource
func (r *MigrationRestoreReconciler) startRestoreProcess(ctx context.Context, statefulMigration *migrationv1.StatefulMigration, sourceCluster string, checkpointBackups []migrationv1.CheckpointBackup) error {
	log := log.FromContext(ctx)

	// Create CheckpointRestore CR for each checkpoint backup
	for _, backup := range checkpointBackups {
		if err := r.createCheckpointRestore(ctx, &backup, statefulMigration); err != nil {
			log.Error(err, "failed to create checkpoint restore", "backup", backup.Name)
			return err
		}
	}

	// Handle resource-specific restore logic
	switch strings.ToLower(statefulMigration.Spec.ResourceRef.Kind) {
	case "pod":
		return r.handlePodRestore(ctx, statefulMigration, checkpointBackups)
	case "statefulset":
		return r.handleStatefulSetRestore(ctx, statefulMigration, checkpointBackups)
	default:
		log.Info("Unsupported resource kind for restore", "kind", statefulMigration.Spec.ResourceRef.Kind)
		return nil
	}
}

// createCheckpointRestore creates a CheckpointRestore CR for the given backup
func (r *MigrationRestoreReconciler) createCheckpointRestore(ctx context.Context, backup *migrationv1.CheckpointBackup, statefulMigration *migrationv1.StatefulMigration) error {
	log := log.FromContext(ctx)

	restoreName := fmt.Sprintf("%s-restore", backup.Name)

	// Check if restore already exists
	var existingRestore migrationv1.CheckpointRestore
	err := r.KarmadaClient.Get(ctx, types.NamespacedName{
		Name:      restoreName,
		Namespace: backup.Namespace,
	}, &existingRestore)

	if err == nil {
		log.Info("CheckpointRestore already exists", "name", restoreName)
		return nil
	}

	if !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check existing CheckpointRestore: %w", err)
	}

	// Create new CheckpointRestore
	restore := &migrationv1.CheckpointRestore{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restoreName,
			Namespace: backup.Namespace,
			Labels: map[string]string{
				"migration.dcnlab.com/restore": "true",
				"migration.dcnlab.com/backup":  backup.Name,
			},
		},
		Spec: migrationv1.CheckpointRestoreSpec{
			BackupRef: migrationv1.BackupRef{
				Name: backup.Name,
			},
			PodName:    backup.Spec.PodRef.Name,
			Containers: backup.Spec.Containers,
		},
	}

	if err := r.KarmadaClient.Create(ctx, restore); err != nil {
		return fmt.Errorf("failed to create CheckpointRestore: %w", err)
	}

	log.Info("Created CheckpointRestore", "name", restoreName, "backup", backup.Name)

	// Create propagation policy for the restore
	return r.createRestorePropagationPolicy(ctx, restore, statefulMigration)
}

// createRestorePropagationPolicy creates a propagation policy for the CheckpointRestore
func (r *MigrationRestoreReconciler) createRestorePropagationPolicy(ctx context.Context, restore *migrationv1.CheckpointRestore, statefulMigration *migrationv1.StatefulMigration) error {
	policyName := fmt.Sprintf("%s-restore-policy", restore.Name)

	// Determine target cluster (first available cluster that's not the source)
	var targetCluster string
	for _, cluster := range statefulMigration.Spec.SourceClusters {
		// For now, we'll use the first cluster that's not the source
		// In a real implementation, you might want to implement more sophisticated logic
		targetCluster = cluster
		break
	}

	if targetCluster == "" {
		return fmt.Errorf("no target cluster available for restore")
	}

	policy := &karmadapolicyv1alpha1.PropagationPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      policyName,
			Namespace: restore.Namespace,
		},
		Spec: karmadapolicyv1alpha1.PropagationSpec{
			ResourceSelectors: []karmadapolicyv1alpha1.ResourceSelector{
				{
					APIVersion: "migration.dcnlab.com/v1",
					Kind:       "CheckpointRestore",
					Name:       restore.Name,
				},
			},
			Placement: karmadapolicyv1alpha1.Placement{
				ClusterAffinity: &karmadapolicyv1alpha1.ClusterAffinity{
					ClusterNames: []string{targetCluster},
				},
			},
		},
	}

	return r.KarmadaClient.CreateOrUpdatePropagationPolicy(ctx, policy)
}

// handlePodRestore handles restore for Pod resources by editing the Work resource
func (r *MigrationRestoreReconciler) handlePodRestore(ctx context.Context, statefulMigration *migrationv1.StatefulMigration, checkpointBackups []migrationv1.CheckpointBackup) error {
	log := log.FromContext(ctx)

	// Find the Work resource for this pod
	work, err := r.findWorkForResource(ctx, statefulMigration.Spec.ResourceRef)
	if err != nil {
		return fmt.Errorf("failed to find Work for pod: %w", err)
	}

	// Update the Work resource to replace container images with checkpoint images
	if err := r.updateWorkWithCheckpointImages(ctx, work, checkpointBackups); err != nil {
		return fmt.Errorf("failed to update Work with checkpoint images: %w", err)
	}

	log.Info("Successfully updated Work with checkpoint images", "work", work.Name)
	return nil
}

// handleStatefulSetRestore handles restore for StatefulSet resources
func (r *MigrationRestoreReconciler) handleStatefulSetRestore(ctx context.Context, statefulMigration *migrationv1.StatefulMigration, checkpointBackups []migrationv1.CheckpointBackup) error {
	log := log.FromContext(ctx)

	// For StatefulSet, we don't edit the Work resource
	// The CheckpointRestore CRs will handle the restore process
	log.Info("StatefulSet restore - CheckpointRestore CRs created, no Work editing needed")
	return nil
}

// findWorkForResource finds the Work resource for a given resource
func (r *MigrationRestoreReconciler) findWorkForResource(ctx context.Context, resourceRef migrationv1.ResourceRef) (*karmadaworkv1alpha1.Work, error) {
	log := log.FromContext(ctx)

	var works karmadaworkv1alpha1.WorkList
	if err := r.KarmadaClient.List(ctx, &works); err != nil {
		return nil, fmt.Errorf("failed to list Work resources: %w", err)
	}

	for _, work := range works.Items {
		// Check if this Work is for our resource
		for _, manifest := range work.Spec.Workload.Manifests {
			var obj unstructured.Unstructured
			if err := obj.UnmarshalJSON(manifest.Raw); err != nil {
				continue
			}

			if obj.GetAPIVersion() == resourceRef.APIVersion &&
				obj.GetKind() == resourceRef.Kind &&
				obj.GetName() == resourceRef.Name &&
				obj.GetNamespace() == resourceRef.Namespace {

				log.Info("Found Work for resource", "work", work.Name, "resource", resourceRef.Name)
				return &work, nil
			}
		}
	}

	return nil, errors.NewNotFound(schema.GroupResource{Group: "work.karmada.io", Resource: "works"}, "not found")
}

// updateWorkWithCheckpointImages updates the Work resource to replace container images with checkpoint images
func (r *MigrationRestoreReconciler) updateWorkWithCheckpointImages(ctx context.Context, work *karmadaworkv1alpha1.Work, checkpointBackups []migrationv1.CheckpointBackup) error {
	log := log.FromContext(ctx)

	// Create a map of container names to checkpoint images
	checkpointImages := make(map[string]string)
	for _, backup := range checkpointBackups {
		for _, container := range backup.Spec.Containers {
			checkpointImages[container.Name] = container.Image
		}
	}

	// Update each manifest in the Work
	for i, manifest := range work.Spec.Workload.Manifests {
		var obj unstructured.Unstructured
		if err := obj.UnmarshalJSON(manifest.Raw); err != nil {
			log.Error(err, "failed to unmarshal manifest", "manifestIndex", i)
			continue
		}

		// Check if this is a Pod resource
		if obj.GetKind() == "Pod" && obj.GetAPIVersion() == "v1" {
			// Update container images
			if err := r.updatePodContainerImages(&obj, checkpointImages); err != nil {
				log.Error(err, "failed to update pod container images", "manifestIndex", i)
				continue
			}

			// Marshal back to JSON
			updatedRaw, err := obj.MarshalJSON()
			if err != nil {
				log.Error(err, "failed to marshal updated manifest", "manifestIndex", i)
				continue
			}

			work.Spec.Workload.Manifests[i].Raw = updatedRaw
		}
	}

	// Update the Work resource
	return r.KarmadaClient.Update(ctx, work)
}

// updatePodContainerImages updates container images in a Pod manifest
func (r *MigrationRestoreReconciler) updatePodContainerImages(pod *unstructured.Unstructured, checkpointImages map[string]string) error {
	// Get containers from the pod spec
	containers, found, err := unstructured.NestedSlice(pod.Object, "spec", "containers")
	if err != nil || !found {
		return fmt.Errorf("failed to get containers from pod spec: %w", err)
	}

	// Update container images
	for i, container := range containers {
		containerMap, ok := container.(map[string]interface{})
		if !ok {
			continue
		}

		containerName, found, err := unstructured.NestedString(containerMap, "name")
		if err != nil || !found {
			continue
		}

		// Check if we have a checkpoint image for this container
		if checkpointImage, exists := checkpointImages[containerName]; exists {
			containerMap["image"] = checkpointImage
			containers[i] = containerMap
		}
	}

	// Set the updated containers back to the pod spec
	if err := unstructured.SetNestedSlice(pod.Object, containers, "spec", "containers"); err != nil {
		return fmt.Errorf("failed to set updated containers: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MigrationRestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Note: We don't watch ResourceBinding resources here because they exist in the Karmada control plane,
	// not in the management cluster where this controller is deployed. Instead, we use the KarmadaClient
	// to list/watch ResourceBindings when processing StatefulMigration resources.
	return ctrl.NewControllerManagedBy(mgr).
		For(&migrationv1.StatefulMigration{}).
		Named("migrationrestore").
		Complete(r)
}
