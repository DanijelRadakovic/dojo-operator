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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DojoSpec defines the desired state of Dojo
type DojoSpec struct {
	// AccountId which owns the Dojo application.
	AccountId string `json:"accountId"`
	// Title of the Dojo application.
	Title string `json:"title"`
	// Database type for the application.
	// +kubebuilder:default:="Postgres"
	Database *Database `json:"database"`
	// CredentialsRef is a reference to Secret which contains the database credentials.
	// Postgres: Reference the Secret which points to the `owner` of cnpg.Database.
	// Mongo: Not yet supported.
	CredentialsRef corev1.SecretReference `json:"credentialsRef"`
	// Replicas is number of application instances to run.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default:=1
	// +optional
	Replicas *int32 `json:"replicas"`
}

// DojoStatus defines the observed state of Dojo.
type DojoStatus struct {
	// Conditions represent the current state of the Dojo resource.
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

	// Credentials represents the Secret that is created for database credentials
	// +optional
	Credentials corev1.SecretReference `json:"credentials,omitempty"`

	// ReadyReplicas is the number of Pods created by the Deployment that have the Ready condition.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// UpdatedReplicas is the number of Pods created by the Deployment that are running the
	// most recent version of the Pod template.
	// +optional
	UpdatedReplicas int32 `json:"updatedReplicas,omitempty"`

	// AvailableReplicas is the number of Pods created by the Deployment that have been
	// ready for at least minReadySeconds.
	// +optional
	AvailableReplicas int32 `json:"availableReplicas,omitempty"`

	// ReadyStatus is a human-readable string representing the ratio of ready replicas
	// to desired replicas (e.g., "1/3"). This is used primarily for display in kubectl.
	// +optional
	ReadyStatus string `json:"readyStatus,omitempty"`

	// Selector is required for HPA to work with CRDs.
	// It must be the string representation of the label selector.
	// +optional
	Selector string `json:"selector,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=dojos
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.readyReplicas,selectorpath=.status.selector
// +kubebuilder:resource:shortName=dj,categories={all}
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.readyStatus"
// +kubebuilder:printcolumn:name="UP-TO-DATE",type="integer",JSONPath=".status.updatedReplicas"
// +kubebuilder:printcolumn:name="AVAILABLE",type="integer",JSONPath=".status.availableReplicas"
// +kubebuilder:printcolumn:name="ACCOUNT",type="string",JSONPath=".spec.accountId"
// +kubebuilder:printcolumn:name="STORAGE",type="string",JSONPath=".spec.storage",priority=1
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// Dojo is the Schema for the dojos API
type Dojo struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of Dojo
	// +required
	Spec DojoSpec `json:"spec"`

	// status defines the observed state of Dojo
	// +optional
	Status DojoStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DojoList contains a list of Dojo
type DojoList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Dojo `json:"items"`
}

// +kubebuilder:validation:Enum=Postgres;Mongo
type Database string

const (
	DatabasePostgres Database = "Postgres"
	DatabaseMongo    Database = "Mongo"
)

func init() {
	SchemeBuilder.Register(&Dojo{}, &DojoList{})
}
