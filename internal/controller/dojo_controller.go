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

	appsv1 "k8s.io/api/apps/v1"
	k8scorev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "github.com/DanijelRadakovic/dojo-operator/api/v1"
)

const (
	// typeAvailableDojo represents the status of the Dojo reconciliation
	typeAvailableDojo = "Available"
	// typeProgressingDojo represents the status used when the Dojo is being reconciled
	typeProgressingDojo = "Progressing"
	// typeDegradedDojo represents the status used when the Dojo has encountered an error
	typeDegradedDojo = "Degraded"

	ReasonReady    = "Ready"
	ReasonFinished = "Finished"
	ReasonHealthy  = "Healthy"

	ReasonInitializing = "Initializing"
	ReasonCreating     = "Creating"
	ReasonScaling      = "Scaling"

	ReasonFailed      = "Failed"
	ReasonStalled     = "Stalled"
	ReasonUnavailable = "Unavailable"
)

// DojoReconciler reconciles a Dojo object
type DojoReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core.jutsu.com,resources=dojos,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.jutsu.com,resources=dojos/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.jutsu.com,resources=dojos/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.0/pkg/reconcile
func (r *DojoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	dojo := &corev1.Dojo{}
	if err := r.Get(ctx, req.NamespacedName, dojo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if len(dojo.Status.Conditions) == 0 {
		meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
			Type:    typeProgressingDojo,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonInitializing,
			Message: "Operator has started reconciling this resource",
		})
		return ctrl.Result{}, r.Status().Update(ctx, dojo)
	}

	// Check if the deployment already exists, if not create a new one
	found := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: dojo.Name, Namespace: dojo.Namespace}, found)

	if apierrors.IsNotFound(err) {
		dep, err := r.deploymentForDojo(dojo)
		if err != nil {
			log.Error(err, "Failed to define Deployment")
			return r.setUnrecoverableErrorStatus(ctx, dojo, ReasonStalled, "Could not define Deployment structure")
		}

		log.Info("Creating Deployment")
		if err = r.Create(ctx, dep); err != nil {
			log.Error(err, "Failed to create Deployment")
			return r.setUnrecoverableErrorStatus(ctx, dojo, ReasonFailed, "Failed to create Deployment in the cluster")
		}

		// Report progress of creation
		return r.setProgressStatus(ctx, dojo, ReasonCreating, "Deployment created; waiting for pods to start")
	} else if err != nil {
		return r.setUnrecoverableErrorStatus(ctx, dojo, ReasonFailed, "Failed to communicate with the cluster")
	}

	desiredReplicas := ptr.Deref(dojo.Spec.Replicas, 0)
	if ptr.Deref(found.Spec.Replicas, 0) != desiredReplicas {
		log.Info("Scaling Deployment", "from", *found.Spec.Replicas, "to", desiredReplicas)

		found.Spec.Replicas = ptr.To(desiredReplicas)
		if err := r.Update(ctx, found); err != nil && !apierrors.IsConflict(err) {
			return r.setUnrecoverableErrorStatus(ctx, dojo, ReasonFailed, "Failed to update Deployment replica count")
		}
		// Return and let the Deployment update event trigger the next loop
		return ctrl.Result{}, nil
	}

	readyReplicas := found.Status.ReadyReplicas
	dojo.Status.ReadyReplicas = readyReplicas
	dojo.Status.UpdatedReplicas = found.Status.UpdatedReplicas
	dojo.Status.AvailableReplicas = found.Status.AvailableReplicas
	dojo.Status.ReadyStatus = fmt.Sprintf("%d/%d", readyReplicas, desiredReplicas)

	// Set Available Condition
	if readyReplicas > 0 {
		meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
			Type:    typeAvailableDojo,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonReady,
			Message: fmt.Sprintf("%d/%d replicas are serving traffic", readyReplicas, desiredReplicas),
		})
	} else {
		meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
			Type:    typeAvailableDojo,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonUnavailable,
			Message: "No replicas are currently ready",
		})
	}

	// Set Progressing Condition
	if readyReplicas == desiredReplicas {
		meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
			Type:    typeProgressingDojo,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonFinished,
			Message: "All replicas are synchronized and ready",
		})
	} else {
		meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
			Type:    typeProgressingDojo,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonScaling,
			Message: fmt.Sprintf("Waiting for replicas: %d/%d ready", readyReplicas, desiredReplicas),
		})
	}

	// Set Degraded Condition (Clear it since we reached the end successfully)
	meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
		Type:    typeDegradedDojo,
		Status:  metav1.ConditionFalse,
		Reason:  ReasonHealthy,
		Message: "Reconciliation successful",
	})

	if err := r.Status().Update(ctx, dojo); err != nil && !apierrors.IsConflict(err) {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// setUnrecoverableErrorStatus sets the Degraded state and returns the error
func (r *DojoReconciler) setUnrecoverableErrorStatus(ctx context.Context, dojo *corev1.Dojo, reason, msg string) (ctrl.Result, error) {
	meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
		Type:    typeDegradedDojo,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: msg,
	})
	// If we hit an unrecoverable error, we aren't progressing anymore
	meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
		Type:   typeProgressingDojo,
		Status: metav1.ConditionFalse,
		Reason: reason,
	})
	err := r.Status().Update(ctx, dojo)
	if err != nil && apierrors.IsConflict(err) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, err
}

// setProgressStatus sets the Progressing state and returns success
func (r *DojoReconciler) setProgressStatus(ctx context.Context, dojo *corev1.Dojo, reason, msg string) (ctrl.Result, error) {
	meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
		Type:    typeProgressingDojo,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: msg,
	})
	// Ensure Degraded is false while we are making progress
	meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
		Type:   typeDegradedDojo,
		Status: metav1.ConditionFalse,
		Reason: reason,
	})

	err := r.Status().Update(ctx, dojo)
	if err != nil && apierrors.IsConflict(err) {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, err
}

func (r *DojoReconciler) labels(name string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "Dojo",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/managed-by": "dojo-operator",
	}
}

func (r *DojoReconciler) labelsSelector(name string) string {
	return labels.SelectorFromSet(r.labels(name)).String()
}

func (r *DojoReconciler) deploymentForDojo(dojo *corev1.Dojo) (*appsv1.Deployment, error) {
	image := "danijelradakovic/dojo:0.1.0-alpine"
	dojoLabels := r.labels(dojo.Name)

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dojo.Name,
			Namespace: dojo.Namespace,
			Labels:    dojoLabels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: dojo.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: dojoLabels,
			},
			Template: k8scorev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: dojoLabels,
				},
				Spec: k8scorev1.PodSpec{
					SecurityContext: &k8scorev1.PodSecurityContext{
						RunAsNonRoot: ptr.To(true),
						SeccompProfile: &k8scorev1.SeccompProfile{
							Type: k8scorev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []k8scorev1.Container{{
						Image: image,
						Name:  "dojo",

						ImagePullPolicy: k8scorev1.PullIfNotPresent,
						// Ensure restrictive context for the container
						// More info: https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted
						SecurityContext: &k8scorev1.SecurityContext{
							RunAsNonRoot:             ptr.To(true),
							RunAsUser:                ptr.To(int64(1001)),
							AllowPrivilegeEscalation: ptr.To(false),
							Capabilities: &k8scorev1.Capabilities{
								Drop: []k8scorev1.Capability{
									"ALL",
								},
							},
						},
						Ports: []k8scorev1.ContainerPort{{
							ContainerPort: 8080,
							Name:          "http",
						}},
						// Application requires credentials to for Postgres database which are not set.
						// Override it with dummy command.
						Command: []string{"sleep", "infinity"},
					}},
				},
			},
		},
	}

	// Set the ownerRef for the Deployment
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(dojo, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DojoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Dojo{}).
		Owns(&appsv1.Deployment{}).
		Named("dojo").
		Complete(r)
}
