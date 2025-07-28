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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	migrationv1 "github.com/lehuannhatrang/stateful-migration-operator/api/v1"
)

const (
	CheckpointBackupFinalizer = "checkpointbackup.migration.dcnlab.com/finalizer"
	CheckpointBasePath        = "/var/lib/kubelet/checkpoints"
	ServiceAccountPath        = "/var/run/secrets/kubernetes.io/serviceaccount"
)

// CheckpointResponse represents the response from kubelet checkpoint API
type CheckpointResponse struct {
	Items []CheckpointItem `json:"items"`
}

type CheckpointItem struct {
	Name           string    `json:"name"`
	CreatedAt      time.Time `json:"createdAt"`
	CheckpointPath string    `json:"checkpointPath"`
}

// CheckpointBackupReconciler reconciles a CheckpointBackup object
type CheckpointBackupReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	NodeName       string
	KubeletClient  *KubeletClient
	RegistryClient *RegistryClient
	Scheduler      *cron.Cron
	scheduledJobs  map[string]cron.EntryID // Track scheduled jobs
}

// KubeletClient handles communication with kubelet API
type KubeletClient struct {
	httpClient *http.Client
	token      string
	kubeletURL string
}

// RegistryClient handles container registry operations
type RegistryClient struct {
	username string
	password string
}

// +kubebuilder:rbac:groups=migration.dcnlab.com,resources=checkpointbackups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migration.dcnlab.com,resources=checkpointbackups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=migration.dcnlab.com,resources=checkpointbackups/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/checkpoint,verbs=patch;create;update;proxy
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop
func (r *CheckpointBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the CheckpointBackup instance
	var checkpointBackup migrationv1.CheckpointBackup
	if err := r.Get(ctx, req.NamespacedName, &checkpointBackup); err != nil {
		if errors.IsNotFound(err) {
			log.Info("CheckpointBackup resource not found. Ignoring since object must be deleted")
			// Clean up any scheduled job
			if r.scheduledJobs != nil {
				if entryID, exists := r.scheduledJobs[req.NamespacedName.String()]; exists {
					r.Scheduler.Remove(entryID)
					delete(r.scheduledJobs, req.NamespacedName.String())
				}
			}
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get CheckpointBackup")
		return ctrl.Result{}, err
	}

	// Initialize clients if not already done
	if err := r.initializeClients(ctx); err != nil {
		log.Error(err, "Failed to initialize clients")
		return ctrl.Result{}, err
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&checkpointBackup, CheckpointBackupFinalizer) {
		controllerutil.AddFinalizer(&checkpointBackup, CheckpointBackupFinalizer)
		if err := r.Update(ctx, &checkpointBackup); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// Handle deletion
	if checkpointBackup.GetDeletionTimestamp() != nil {
		return r.reconcileDelete(ctx, &checkpointBackup)
	}

	// Check if the pod is on this node
	isOnThisNode, err := r.isPodOnThisNode(ctx, &checkpointBackup)
	if err != nil {
		log.Error(err, "Failed to check if pod is on this node")
		return ctrl.Result{}, err
	}

	if !isOnThisNode {
		log.Info("Pod is not on this node, skipping", "pod", checkpointBackup.Spec.PodRef.Name, "node", r.NodeName)
		return ctrl.Result{}, nil
	}

	// Handle normal reconciliation
	return r.reconcileNormal(ctx, &checkpointBackup)
}

// initializeClients initializes the kubelet and registry clients
func (r *CheckpointBackupReconciler) initializeClients(ctx context.Context) error {
	if r.KubeletClient == nil {
		kubeletClient, err := NewKubeletClient()
		if err != nil {
			return fmt.Errorf("failed to create kubelet client: %w", err)
		}
		r.KubeletClient = kubeletClient
	}

	if r.RegistryClient == nil {
		registryClient, err := r.NewRegistryClient(ctx)
		if err != nil {
			return fmt.Errorf("failed to create registry client: %w", err)
		}
		r.RegistryClient = registryClient
	}

	if r.Scheduler == nil {
		r.Scheduler = cron.New()
		r.Scheduler.Start()
		r.scheduledJobs = make(map[string]cron.EntryID)
	}

	return nil
}

// NewKubeletClient creates a new kubelet client
func NewKubeletClient() (*KubeletClient, error) {
	// Read service account token
	tokenBytes, err := os.ReadFile(filepath.Join(ServiceAccountPath, "token"))
	if err != nil {
		return nil, fmt.Errorf("failed to read service account token: %w", err)
	}

	// Get node IP from environment or use localhost
	kubeletURL := "https://localhost:10250"
	if nodeIP := os.Getenv("NODE_IP"); nodeIP != "" {
		kubeletURL = fmt.Sprintf("https://%s:10250", nodeIP)
	}

	// Create HTTP client with TLS config (skip verification for kubelet)
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 30 * time.Second,
	}

	return &KubeletClient{
		httpClient: httpClient,
		token:      string(tokenBytes),
		kubeletURL: kubeletURL,
	}, nil
}

// NewRegistryClient creates a new registry client
func (r *CheckpointBackupReconciler) NewRegistryClient(ctx context.Context) (*RegistryClient, error) {
	// Get registry credentials from secret
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Name:      "registry-credentials",
		Namespace: "stateful-migration",
	}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get registry credentials secret: %w", err)
	}

	username := string(secret.Data["username"])
	password := string(secret.Data["password"])

	if username == "" || password == "" {
		return nil, fmt.Errorf("registry credentials are empty")
	}

	return &RegistryClient{
		username: username,
		password: password,
	}, nil
}

// isPodOnThisNode checks if the pod referenced in CheckpointBackup is on this node
func (r *CheckpointBackupReconciler) isPodOnThisNode(ctx context.Context, backup *migrationv1.CheckpointBackup) (bool, error) {
	var pod corev1.Pod
	if err := r.Get(ctx, types.NamespacedName{
		Name:      backup.Spec.PodRef.Name,
		Namespace: backup.Spec.PodRef.Namespace,
	}, &pod); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	return pod.Spec.NodeName == r.NodeName, nil
}

// reconcileNormal handles the normal reconciliation logic
func (r *CheckpointBackupReconciler) reconcileNormal(ctx context.Context, backup *migrationv1.CheckpointBackup) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Schedule checkpoint creation based on the schedule
	backupKey := types.NamespacedName{
		Name:      backup.Name,
		Namespace: backup.Namespace,
	}.String()

	// Remove existing job if schedule changed
	if entryID, exists := r.scheduledJobs[backupKey]; exists {
		r.Scheduler.Remove(entryID)
		delete(r.scheduledJobs, backupKey)
	}

	// Add new scheduled job
	entryID, err := r.Scheduler.AddFunc(backup.Spec.Schedule, func() {
		if err := r.performCheckpoint(context.Background(), backup); err != nil {
			log.Error(err, "Failed to perform checkpoint", "backup", backup.Name)
		}
	})
	if err != nil {
		log.Error(err, "Failed to schedule checkpoint job")
		return ctrl.Result{}, err
	}

	r.scheduledJobs[backupKey] = entryID
	log.Info("Scheduled checkpoint job", "backup", backup.Name, "schedule", backup.Spec.Schedule)

	// Also perform immediate checkpoint on first reconcile
	if backup.Status.LastCheckpointTime == nil {
		if err := r.performCheckpoint(ctx, backup); err != nil {
			log.Error(err, "Failed to perform initial checkpoint")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{RequeueAfter: time.Hour}, nil
}

// reconcileDelete handles the deletion logic
func (r *CheckpointBackupReconciler) reconcileDelete(ctx context.Context, backup *migrationv1.CheckpointBackup) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Remove scheduled job
	backupKey := types.NamespacedName{
		Name:      backup.Name,
		Namespace: backup.Namespace,
	}.String()

	if entryID, exists := r.scheduledJobs[backupKey]; exists {
		r.Scheduler.Remove(entryID)
		delete(r.scheduledJobs, backupKey)
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(backup, CheckpointBackupFinalizer)
	if err := r.Update(ctx, backup); err != nil {
		log.Error(err, "Failed to remove finalizer")
		return ctrl.Result{}, err
	}

	log.Info("Successfully deleted CheckpointBackup", "name", backup.Name)
	return ctrl.Result{}, nil
}

// performCheckpoint performs the actual checkpoint operation
func (r *CheckpointBackupReconciler) performCheckpoint(ctx context.Context, backup *migrationv1.CheckpointBackup) error {
	log := logf.FromContext(ctx)
	log.Info("Starting checkpoint operation", "backup", backup.Name, "pod", backup.Spec.PodRef.Name)

	// Get pod to ensure it exists and is ready
	var pod corev1.Pod
	if err := r.Get(ctx, types.NamespacedName{
		Name:      backup.Spec.PodRef.Name,
		Namespace: backup.Spec.PodRef.Namespace,
	}, &pod); err != nil {
		return fmt.Errorf("failed to get pod: %w", err)
	}

	if pod.Status.Phase != corev1.PodRunning {
		log.Info("Pod is not running, skipping checkpoint", "pod", pod.Name, "phase", pod.Status.Phase)
		return nil
	}

	// Process each container
	for _, container := range backup.Spec.Containers {
		if err := r.checkpointContainer(ctx, backup, &pod, container); err != nil {
			log.Error(err, "Failed to checkpoint container", "container", container.Name)
			return err
		}
	}

	// Update status
	now := metav1.Now()
	backup.Status.LastCheckpointTime = &now
	backup.Status.Phase = "Completed"
	if err := r.Status().Update(ctx, backup); err != nil {
		log.Error(err, "Failed to update backup status")
		return err
	}

	log.Info("Successfully completed checkpoint operation", "backup", backup.Name)
	return nil
}

// checkpointContainer performs checkpoint operation for a single container
func (r *CheckpointBackupReconciler) checkpointContainer(ctx context.Context, backup *migrationv1.CheckpointBackup, pod *corev1.Pod, container migrationv1.Container) error {
	log := logf.FromContext(ctx)
	log.Info("Checkpointing container", "container", container.Name, "pod", pod.Name)

	// Step 1: Call kubelet checkpoint API
	checkpointPath, err := r.KubeletClient.CreateCheckpoint(backup.Spec.PodRef.Namespace, backup.Spec.PodRef.Name, container.Name)
	if err != nil {
		return fmt.Errorf("failed to create checkpoint via kubelet API: %w", err)
	}

	// Step 2: Get the original container image
	var baseImage string
	for _, c := range pod.Spec.Containers {
		if c.Name == container.Name {
			baseImage = c.Image
			break
		}
	}
	if baseImage == "" {
		return fmt.Errorf("could not find base image for container %s", container.Name)
	}

	// Step 3: Build checkpoint image using buildah
	if err := r.buildCheckpointImage(checkpointPath, container.Image, baseImage, container.Name); err != nil {
		return fmt.Errorf("failed to build checkpoint image: %w", err)
	}

	// Step 4: Push image to registry
	if err := r.RegistryClient.PushImage(container.Image); err != nil {
		return fmt.Errorf("failed to push checkpoint image: %w", err)
	}

	log.Info("Successfully checkpointed and pushed container image", "container", container.Name, "image", container.Image)
	return nil
}

// CreateCheckpoint calls kubelet checkpoint API
func (kc *KubeletClient) CreateCheckpoint(namespace, podName, containerName string) (string, error) {
	url := fmt.Sprintf("%s/checkpoint/%s/%s/%s?timeout=60", kc.kubeletURL, namespace, podName, containerName)

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+kc.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := kc.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to call kubelet checkpoint API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("kubelet checkpoint API returned status %d: %s", resp.StatusCode, string(body))
	}

	var checkpointResp CheckpointResponse
	if err := json.NewDecoder(resp.Body).Decode(&checkpointResp); err != nil {
		return "", fmt.Errorf("failed to decode checkpoint response: %w", err)
	}

	if len(checkpointResp.Items) == 0 {
		return "", fmt.Errorf("no checkpoint items returned")
	}

	return checkpointResp.Items[0].CheckpointPath, nil
}

// buildCheckpointImage builds the checkpoint image using buildah
func (r *CheckpointBackupReconciler) buildCheckpointImage(checkpointPath, imageName, baseImage, containerName string) error {
	log := logf.FromContext(context.Background())

	// Verify checkpoint file exists
	fullCheckpointPath := filepath.Join(CheckpointBasePath, checkpointPath)
	if _, err := os.Stat(fullCheckpointPath); os.IsNotExist(err) {
		return fmt.Errorf("checkpoint file does not exist: %s", fullCheckpointPath)
	}

	// Step 1: Create new container from scratch
	cmd := exec.Command("buildah", "from", "scratch")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to create buildah container: %w", err)
	}
	newContainer := strings.TrimSpace(string(out))

	// Ensure cleanup
	defer func() {
		exec.Command("buildah", "rm", newContainer).Run()
	}()

	// Step 2: Add checkpoint tar to root
	if err := exec.Command("buildah", "add", newContainer, fullCheckpointPath, "/").Run(); err != nil {
		return fmt.Errorf("failed to add checkpoint to container: %w", err)
	}

	// Step 3: Add CRI-O checkpoint annotations
	if err := exec.Command("buildah", "config",
		"--annotation=io.kubernetes.cri-o.annotations.checkpoint.name="+imageName,
		newContainer).Run(); err != nil {
		return fmt.Errorf("failed to add checkpoint name annotation: %w", err)
	}

	if err := exec.Command("buildah", "config",
		"--annotation=io.kubernetes.cri-o.annotations.checkpoint.rootfsImageName="+baseImage,
		newContainer).Run(); err != nil {
		return fmt.Errorf("failed to add rootfs image annotation: %w", err)
	}

	// Step 4: Commit and tag image
	if err := exec.Command("buildah", "commit", newContainer, imageName).Run(); err != nil {
		return fmt.Errorf("failed to commit image: %w", err)
	}

	log.Info("Successfully built checkpoint image", "image", imageName, "baseImage", baseImage)
	return nil
}

// PushImage pushes the image to the registry
func (rc *RegistryClient) PushImage(imageName string) error {
	// Login to registry
	if err := rc.login(imageName); err != nil {
		return fmt.Errorf("failed to login to registry: %w", err)
	}

	// Push image
	cmd := exec.Command("buildah", "push", imageName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to push image %s: %w", imageName, err)
	}

	return nil
}

// login performs registry authentication
func (rc *RegistryClient) login(imageName string) error {
	// Extract registry from image name
	parts := strings.Split(imageName, "/")
	if len(parts) < 2 {
		return fmt.Errorf("invalid image name format: %s", imageName)
	}

	registry := parts[0]

	// Login using buildah
	cmd := exec.Command("buildah", "login", "-u", rc.username, "-p", rc.password, registry)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to login to registry %s: %w", registry, err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager
func (r *CheckpointBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Get node name from environment
	r.NodeName = os.Getenv("NODE_NAME")
	if r.NodeName == "" {
		return fmt.Errorf("NODE_NAME environment variable is required")
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&migrationv1.CheckpointBackup{}).
		Named("checkpointbackup").
		Complete(r)
}
