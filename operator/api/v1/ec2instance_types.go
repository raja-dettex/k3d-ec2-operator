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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="State",type="string",JSONPath=".status.state",description="Current phase of the EC2 instance"
// +kubebuilder:printcolumn:name="Instance-ID",type="string",JSONPath=".status.instanceId",description="AWS EC2 Instance ID"
// +kubebuilder:printcolumn:name="Public-IP",type="string",JSONPath=".status.publicIP",description="Public IP assigned to the instance"

// EC2InstanceSpec defines the desired state of EC2Instance
type EC2InstanceSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// foo is an example field of EC2Instance. Edit ec2instance_types.go to remove/update
	// +optional

	AMIid            string            `json:"amiId"`
	KeyPair          string            `json:"keyPair"`
	Region           string            `json:"region"`
	AvailabilityZone string            `json:"availabilityZone"`
	InstanceType     string            `json:"instanceType"`
	Subnet           string            `json:"subnet"`
	Tags             map[string]string `json:"tags,omitempty"`
	SecurityGroups   []string          `json:"securityGroups,omitempty"`
	UserData         string            `json:"userData,omitempty"`
	Storage          StorageConfig     `json:"storage"`
}

type StorageConfig struct {
	RootVolume        VolumeConfig   `json:"rootVolume"`
	AdditionalVolumes []VolumeConfig `json:"additionalVolumes,omitempty"`
}

type VolumeConfig struct {
	Size string `json:"size"`
	Type string `json:"type"`
}

// EC2InstanceStatus defines the observed state of EC2Instance.
type EC2InstanceStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the EC2Instance resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional

	Conditions []metav1.Condition `json:"conditions,omitempty"`
	State      string             `json:"state"`
	InstanceId string             `json:"instanceId,omitempty"`
	PublicIP   string             `json:"publicIP,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// EC2Instance is the Schema for the ec2instances API
type EC2Instance struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of EC2Instance
	// +required
	Spec EC2InstanceSpec `json:"spec"`

	// status defines the observed state of EC2Instance
	// +optional
	Status EC2InstanceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// EC2InstanceList contains a list of EC2Instance
type EC2InstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []EC2Instance `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &EC2Instance{}, &EC2InstanceList{})
		return nil
	})
}
