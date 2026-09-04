/*
Copyright 2026.

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
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	//computev1 "domain.com/cloud_resources/api/v1"

	computev1 "github.com/raja-dettex/k3d-ec2-operator/operator/api/v1"
	// AWS SDK imports
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// EC2InstanceReconciler reconciles a EC2Instance object
type EC2InstanceReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	EC2Client *ec2.Client
	Recorder  record.EventRecorder
}

const ec2Finalizer = "compute.example.com/finalizer"

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the EC2Instance object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.24.1/pkg/reconcile

// +kubebuilder:rbac:groups=compute.domain.com,resources=ec2instances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compute.domain.com,resources=ec2instances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=compute.domain.com,resources=ec2instances/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch;update
// +kubebuilder:rbac:groups="events.k8s.io",resources=events,verbs=create;patch;update
func (r *EC2InstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	ec2Instance := &computev1.EC2Instance{}
	if err := r.Get(ctx, req.NamespacedName, ec2Instance); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("EC2Instance resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// 1. Handle Deletion Logic via Finalizer
	isMarkedToBeDeleted := ec2Instance.GetDeletionTimestamp() != nil
	if isMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(ec2Instance, ec2Finalizer) {
			r.Recorder.Eventf(ec2Instance, corev1.EventTypeNormal, "Deleting", "Terminating EC2 instance %s in AWS emulator", ec2Instance.Status.InstanceId)
			if err := r.finalizeEC2Instance(ctx, ec2Instance); err != nil {
				r.Recorder.Eventf(ec2Instance, corev1.EventTypeWarning, "DeletionFailed", "Failed to terminate EC2 instance: %v", err)
				return ctrl.Result{}, err
			}
			r.Recorder.Event(ec2Instance, corev1.EventTypeNormal, "Deleted", "EC2 instance terminated successfully")
			controllerutil.RemoveFinalizer(ec2Instance, ec2Finalizer)
			err := r.Update(ctx, ec2Instance)
			if err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, nil
	}

	// 2. Add Finalizer if missing
	if !controllerutil.ContainsFinalizer(ec2Instance, ec2Finalizer) {
		controllerutil.AddFinalizer(ec2Instance, ec2Finalizer)
		if err := r.Update(ctx, ec2Instance); err != nil {
			return ctrl.Result{}, err
		}
	}

	// 3. Provisioning Logic (Only reached after finalizer is committed)
	if ec2Instance.Status.InstanceId == "" {
		logger.Info("Creating EC2 Instance in emulator")
		r.Recorder.Event(ec2Instance, corev1.EventTypeNormal, "Creating", "Provisioning EC2 Instance in AWS emulator")
		instanceID, publicIP, err := r.createEC2InEmulator(ctx, ec2Instance)
		if err != nil {
			r.Recorder.Eventf(ec2Instance, corev1.EventTypeWarning, "CreationFailed", "Failed to create EC2 instance: %v", err)
			_ = r.updateStatusCondition(ctx, ec2Instance, "Progressing", metav1.ConditionFalse, "CreationFailed", err.Error())
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}

		// emit success event
		r.Recorder.Eventf(ec2Instance, corev1.EventTypeNormal, "Created", "EC2 instance %s created successfully with IP %s", instanceID, publicIP)

		if err := r.updateStatusSuccess(ctx, ec2Instance, instanceID, publicIP); err != nil {
			logger.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
	}

	return r.syncInstanceStatus(ctx, ec2Instance)
}

func (r *EC2InstanceReconciler) updateStatusSuccess(ctx context.Context, instance *computev1.EC2Instance, instanceID, publicIP string) error {
	patch := client.MergeFrom(instance.DeepCopy())

	instance.Status.InstanceId = instanceID
	instance.Status.PublicIP = publicIP
	instance.Status.State = "Running"
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Provisioned",
		Message:            fmt.Sprintf("EC2 Instance %s successfully created", instanceID),
		LastTransitionTime: metav1.Now(),
	})

	return r.Status().Patch(ctx, instance, patch)
}

func (r *EC2InstanceReconciler) finalizeEC2Instance(ctx context.Context, instance *computev1.EC2Instance) error {
	if instance.Status.InstanceId == "" {
		return nil // No instance ID provisioned, nothing to terminate in emulator
	}

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []string{instance.Status.InstanceId},
	}

	_, err := r.EC2Client.TerminateInstances(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to terminate instance %s: %w", instance.Status.InstanceId, err)
	}

	return nil
}

func (r *EC2InstanceReconciler) createEC2InEmulator(ctx context.Context, instance *computev1.EC2Instance) (string, string, error) {
	// 1. Build EBS Mappings from your exact Storage struct
	blockDeviceMappings, err := buildBlockDeviceMappings(&instance.Spec.Storage)
	if err != nil {
		return "", "", fmt.Errorf("failed to build storage config: %w", err)
	}

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(instance.Spec.AMIid),
		InstanceType: types.InstanceType(instance.Spec.InstanceType),
		SubnetId:     &instance.Spec.Subnet,
		Placement: &types.Placement{
			AvailabilityZoneId: &instance.Spec.AvailabilityZone,
		},
		MinCount:            aws.Int32(1),
		MaxCount:            aws.Int32(1),
		BlockDeviceMappings: blockDeviceMappings,
	}

	if instance.Spec.KeyPair != "" {
		input.KeyName = aws.String(instance.Spec.KeyPair)
	}

	result, err := r.EC2Client.RunInstances(ctx, input)
	if err != nil {
		return "", "", fmt.Errorf("failed to run instance in AWS emulator: %w", err)
	}

	if len(result.Instances) == 0 {
		return "", "", fmt.Errorf("no instances returned by AWS emulator")
	}

	createdInstance := result.Instances[0]
	instanceID := aws.ToString(createdInstance.InstanceId)

	publicIP := ""
	if createdInstance.PublicIpAddress != nil {
		publicIP = aws.ToString(createdInstance.PublicIpAddress)
	}

	return instanceID, publicIP, nil
}

func (r *EC2InstanceReconciler) updateStatusCondition(ctx context.Context, instance *computev1.EC2Instance, condType string, status metav1.ConditionStatus, reason, message string) error {
	// 1. Create a deep copy to establish the baseline for the diff
	patch := client.MergeFrom(instance.DeepCopy())

	// 2. Modify status on the local object
	meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})

	// 3. Patch sends only the delta (bypasses resourceVersion conflicts)
	return r.Status().Patch(ctx, instance, patch)
}

// syncInstanceStatus polls AWS EC2 and updates CRD status via Patch
func (r *EC2InstanceReconciler) syncInstanceStatus(ctx context.Context, instance *computev1.EC2Instance) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// 1. Query AWS/Floci for current state
	describeOutput, err := r.EC2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instance.Status.InstanceId},
	})
	if err != nil {
		logger.Error(err, "Failed to describe EC2 instance", "instanceId", instance.Status.InstanceId)
		_ = r.updateStatusCondition(ctx, instance, "Ready", metav1.ConditionFalse, "DescribeFailed", err.Error())
		// Requeue after 10s to retry on transient AWS errors
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if len(describeOutput.Reservations) == 0 || len(describeOutput.Reservations[0].Instances) == 0 {
		logger.Info("Instance not found in AWS emulator", "instanceId", instance.Status.InstanceId)
		_ = r.updateStatusCondition(ctx, instance, "Ready", metav1.ConditionFalse, "InstanceNotFound", "Instance missing in provider")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	ec2Data := describeOutput.Reservations[0].Instances[0]
	currentState := string(ec2Data.State.Name)
	publicIP := aws.ToString(ec2Data.PublicIpAddress)

	// 2. Prepare status patch
	patch := client.MergeFrom(instance.DeepCopy())
	instance.Status.State = currentState
	instance.Status.PublicIP = publicIP

	// Update conditions based on AWS instance state
	switch ec2Data.State.Name {
	case types.InstanceStateNameRunning:
		meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "InstanceRunning",
			Message:            fmt.Sprintf("EC2 instance %s is running", instance.Status.InstanceId),
			LastTransitionTime: metav1.Now(),
		})

	case types.InstanceStateNamePending:
		meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "InstancePending",
			Message:            "EC2 instance is initializing",
			LastTransitionTime: metav1.Now(),
		})

	default: // stopping, stopped, terminated
		meta.SetStatusCondition(&instance.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "InstanceNotReady",
			Message:            fmt.Sprintf("EC2 instance state is %s", currentState),
			LastTransitionTime: metav1.Now(),
		})
	}

	if err := r.Status().Patch(ctx, instance, patch); err != nil {
		logger.Error(err, "Failed to patch instance status during polling")
		return ctrl.Result{}, err
	}

	// 3. Determine Requeue Strategy
	// If state is still transitioning, poll frequently (every 5 seconds)
	if ec2Data.State.Name == types.InstanceStateNamePending {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// If fully running, poll periodically to catch external changes/drift (every 30 seconds)
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// parseVolumeSize converts string sizes like "20", "20Gi", or "20G" to int32 GiB
func parseVolumeSize(sizeStr string) (int32, error) {
	cleanStr := strings.TrimSuffix(strings.TrimSuffix(sizeStr, "Gi"), "G")
	sizeInt, err := strconv.Atoi(cleanStr)
	if err != nil {
		return 0, fmt.Errorf("invalid volume size format %q: %w", sizeStr, err)
	}
	return int32(sizeInt), nil
}

// buildBlockDeviceMappings maps your StorageConfig struct into AWS SDK BlockDeviceMapping types
func buildBlockDeviceMappings(storage *computev1.StorageConfig) ([]types.BlockDeviceMapping, error) {
	if storage == nil {
		return nil, nil
	}

	var mappings []types.BlockDeviceMapping

	// 1. Root Volume Mapping (/dev/sda1)
	if storage.RootVolume.Size != "" {
		sizeGiB, err := parseVolumeSize(storage.RootVolume.Size)
		if err != nil {
			return nil, fmt.Errorf("root volume error: %w", err)
		}

		volType := storage.RootVolume.Type
		if volType == "" {
			volType = "gp3" // Default fallback
		}

		mappings = append(mappings, types.BlockDeviceMapping{
			DeviceName: aws.String("/dev/sda1"),
			Ebs: &types.EbsBlockDevice{
				VolumeSize:          aws.Int32(sizeGiB),
				VolumeType:          types.VolumeType(volType),
				DeleteOnTermination: aws.Bool(true),
			},
		})
	}

	// 2. Additional Volumes Mapping (/dev/sdb, /dev/sdc, /dev/sdd, ...)
	deviceLetters := []string{"b", "c", "d", "e", "f", "g", "h"}
	for i, vol := range storage.AdditionalVolumes {
		if i >= len(deviceLetters) {
			return nil, fmt.Errorf("exceeded maximum supported additional volumes limit (%d)", len(deviceLetters))
		}

		sizeGiB, err := parseVolumeSize(vol.Size)
		if err != nil {
			return nil, fmt.Errorf("additional volume [%d] error: %w", i, err)
		}

		volType := vol.Type
		if volType == "" {
			volType = "gp3"
		}

		deviceName := fmt.Sprintf("/dev/sd%s", deviceLetters[i])

		mappings = append(mappings, types.BlockDeviceMapping{
			DeviceName: aws.String(deviceName),
			Ebs: &types.EbsBlockDevice{
				VolumeSize:          aws.Int32(sizeGiB),
				VolumeType:          types.VolumeType(volType),
				DeleteOnTermination: aws.Bool(true),
			},
		})
	}

	return mappings, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EC2InstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&computev1.EC2Instance{}).
		Named("ec2instance").
		Complete(r)
}

// prevent unused import removal by goimports/gopls — kept intentionally
var _awsImportsUsed = func() bool {
	var _ aws.Config
	var _ = config.LoadDefaultConfig
	var _ = credentials.NewStaticCredentialsProvider
	var _ = ec2.NewFromConfig
	var _ types.Instance
	return true
}()
