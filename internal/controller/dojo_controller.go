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
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "github.com/DanijelRadakovic/dojo-operator/api/v1"
)

const (
	// ConditionAvailableDojo represents the status of the Dojo reconciliation
	ConditionAvailableDojo = "Available"
	// ConditionProgressingDojo represents the status used when the Dojo is being reconciled
	ConditionProgressingDojo = "Progressing"
	// ConditionDegradedDojo represents the status used when the Dojo has encountered an error
	ConditionDegradedDojo = "Degraded"

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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.0/pkg/reconcile
func (r *DojoReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	dojo := &corev1.Dojo{}
	if err := r.Get(ctx, req.NamespacedName, dojo); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if len(dojo.Status.Conditions) == 0 {
		meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
			Type:    ConditionProgressingDojo,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonInitializing,
			Message: "Operator has started reconciling this resource",
		})
		return ctrl.Result{}, r.Status().Update(ctx, dojo)
	}

	if result, err := r.reconcileCredentials(ctx, dojo); err != nil || result != nil {
		return *result, err
	}

	result, err := r.reconcileDeployment(ctx, dojo)
	if result == nil {
		return ctrl.Result{}, err
	}
	return *result, err
}

func (r *DojoReconciler) reconcileCredentials(ctx context.Context, dojo *corev1.Dojo) (*ctrl.Result, error) {
	log := logf.FromContext(ctx)
	// Check if the secret already exists
	appSecretName := dojo.Name + "-credentials"
	err := r.Get(ctx, types.NamespacedName{Name: appSecretName, Namespace: dojo.Namespace}, &k8scorev1.Secret{})

	if apierrors.IsNotFound(err) {
		secret := &k8scorev1.Secret{}
		err = r.Get(ctx, types.NamespacedName{Name: dojo.Spec.CredentialsRef.Name, Namespace: dojo.Spec.CredentialsRef.Namespace}, secret)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Error(err, "Failed to find credentials secret")
				return r.setUnrecoverableErrorStatus(ctx, dojo, ReasonFailed, "Failed to find credentials secret")
			}
			return &ctrl.Result{}, err
		}

		appSecret := &k8scorev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      appSecretName,
				Namespace: dojo.Namespace,
				Labels:    r.labels(dojo.Name),
			},
			Type: k8scorev1.SecretTypeOpaque,
		}

		if ptr.Deref(dojo.Spec.Database, "") == corev1.DatabasePostgres {
			// The CNPG Database resource with correct owner should be present in cluster.
			// The secret contains several properties, in our case we are using the `uri` property which
			// contains full URI to the database.
			appSecret.Data = map[string][]byte{
				"DB_ENDPOINT": secret.Data["host"],
				"DB_PORT":     secret.Data["port"],
				"DB_NAME":     secret.Data["dbname"],
				"DB_USER":     secret.Data["user"],
				"DB_PASS":     secret.Data["password"],
			}
		} else if ptr.Deref(dojo.Spec.Database, "") == corev1.DatabaseMongo {
			// Integrate with the MongoDB operator: https://www.mongodb.com/docs/kubernetes/current/
			log.Error(err, "Mongo database is not yet supported")
			return r.setUnrecoverableErrorStatus(ctx, dojo, ReasonFailed, "Mongo database is not yet supported")
		}

		// Set the Dojo as the owner so the secret is deleted when the Dojo is deleted
		if err = controllerutil.SetControllerReference(dojo, appSecret, r.Scheme); err != nil {
			log.Error(err, "Failed to define Secret ownership")
			return r.setUnrecoverableErrorStatus(ctx, dojo, ReasonStalled, "Could not define Secret ownership")
		}

		log.Info("Creating Secret")
		if err = r.Create(ctx, appSecret); err != nil && !apierrors.IsAlreadyExists(err) {
			log.Error(err, "Failed to create Secret")
			return r.setUnrecoverableErrorStatus(ctx, dojo, ReasonFailed, "Failed to create Secret in the cluster")
		}

		dojo.Status.Credentials = k8scorev1.SecretReference{Name: appSecret.Name, Namespace: appSecret.Namespace}
		return r.setProgressStatus(ctx, dojo, ReasonCreating, "Credentials secret created")
	} else if err != nil {
		return r.setUnrecoverableErrorStatus(ctx, dojo, ReasonFailed, "Failed to communicate with the cluster")
	}

	// There are scenarios when secret is created but the status for the dojo is not updated. Here are the examples:
	// - The secret is created but the update for dojo status failed, because API could not be reached.
	// - The secret is created, the dojo status is updated but the change is not yet propagated.
	// Because of that, we need to check and reupdate the status and conditions, also not progressing to next step of reconciliation.
	if dojo.Status.Credentials == (k8scorev1.SecretReference{}) {
		dojo.Status.Credentials = k8scorev1.SecretReference{Name: appSecretName, Namespace: dojo.Namespace}
		return r.setProgressStatus(ctx, dojo, ReasonCreating, "Credentials secret created")
	}
	return nil, nil
}

func (r *DojoReconciler) reconcileDeployment(ctx context.Context, dojo *corev1.Dojo) (*ctrl.Result, error) {
	log := logf.FromContext(ctx)
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
		return &ctrl.Result{}, nil
	}

	readyReplicas := found.Status.ReadyReplicas
	dojo.Status.ReadyReplicas = readyReplicas
	dojo.Status.Selector = r.labelsSelector(dojo.Name)
	dojo.Status.UpdatedReplicas = found.Status.UpdatedReplicas
	dojo.Status.AvailableReplicas = found.Status.AvailableReplicas
	dojo.Status.ReadyStatus = fmt.Sprintf("%d/%d", readyReplicas, desiredReplicas)

	// Set Available Condition
	if readyReplicas > 0 {
		meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
			Type:    ConditionAvailableDojo,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonReady,
			Message: fmt.Sprintf("%d/%d replicas are serving traffic", readyReplicas, desiredReplicas),
		})
	} else {
		meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
			Type:    ConditionAvailableDojo,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonUnavailable,
			Message: "No replicas are currently ready",
		})
	}

	// Set Progressing Condition
	if readyReplicas == desiredReplicas {
		meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
			Type:    ConditionProgressingDojo,
			Status:  metav1.ConditionFalse,
			Reason:  ReasonFinished,
			Message: "All replicas are synchronized and ready",
		})
	} else {
		meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
			Type:    ConditionProgressingDojo,
			Status:  metav1.ConditionTrue,
			Reason:  ReasonScaling,
			Message: fmt.Sprintf("Waiting for replicas: %d/%d ready", readyReplicas, desiredReplicas),
		})
	}

	// Set Degraded Condition (Clear it since we reached the end successfully)
	meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
		Type:    ConditionDegradedDojo,
		Status:  metav1.ConditionFalse,
		Reason:  ReasonHealthy,
		Message: "Reconciliation successful",
	})

	if err := r.Status().Update(ctx, dojo); err != nil && !apierrors.IsConflict(err) {
		return &ctrl.Result{}, err
	}

	return &ctrl.Result{}, nil
}

// setUnrecoverableErrorStatus sets the Degraded state and returns the error
func (r *DojoReconciler) setUnrecoverableErrorStatus(ctx context.Context, dojo *corev1.Dojo, reason, msg string) (*ctrl.Result, error) {
	meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
		Type:    ConditionDegradedDojo,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: msg,
	})
	// If we hit an unrecoverable error, we aren't progressing anymore
	meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
		Type:   ConditionProgressingDojo,
		Status: metav1.ConditionFalse,
		Reason: reason,
	})
	err := r.Status().Update(ctx, dojo)
	if err != nil && apierrors.IsConflict(err) {
		return &ctrl.Result{}, nil
	}
	return &ctrl.Result{}, err
}

// setProgressStatus sets the Progressing state and returns success
func (r *DojoReconciler) setProgressStatus(ctx context.Context, dojo *corev1.Dojo, reason, msg string) (*ctrl.Result, error) {
	meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
		Type:    ConditionProgressingDojo,
		Status:  metav1.ConditionTrue,
		Reason:  reason,
		Message: msg,
	})
	// Ensure Degraded is false while we are making progress
	meta.SetStatusCondition(&dojo.Status.Conditions, metav1.Condition{
		Type:   ConditionDegradedDojo,
		Status: metav1.ConditionFalse,
		Reason: reason,
	})

	err := r.Status().Update(ctx, dojo)
	if err != nil && apierrors.IsConflict(err) {
		return &ctrl.Result{}, nil
	}
	return &ctrl.Result{}, err
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
						EnvFrom: []k8scorev1.EnvFromSource{
							{
								SecretRef: &k8scorev1.SecretEnvSource{
									LocalObjectReference: k8scorev1.LocalObjectReference{Name: dojo.Status.Credentials.Name},
									Optional:             ptr.To(false),
								},
							},
						},
						WorkingDir: "/tmp", // to write traces.json
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

func (r *DojoReconciler) findDojosForSecret(ctx context.Context, obj client.Object) []reconcile.Request {
	secret, ok := obj.(*k8scorev1.Secret)
	if !ok {
		return nil
	}

	// Check if this secret belongs to a CNPG cluster
	_, isCnpg := secret.Labels["cnpg.io/cluster"]
	if !isCnpg {
		return nil
	}

	// Match this secret back to your Dojo.
	// Query all Dojos to see which one references this cluster
	dojos := &corev1.DojoList{}
	err := r.List(ctx, dojos)
	if err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, dojo := range dojos.Items {
		// Only trigger if this Shop is supposed to use this specific DB cluster
		if ptr.Deref(dojo.Spec.Database, "") == corev1.DatabasePostgres &&
			dojo.Spec.CredentialsRef.Name == secret.Name &&
			dojo.Spec.CredentialsRef.Namespace == secret.Namespace {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      dojo.Name,
					Namespace: dojo.Namespace,
				},
			})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *DojoReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Dojo{}).
		Owns(&appsv1.Deployment{}).
		Owns(&k8scorev1.Secret{}).
		Watches(
			&k8scorev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findDojosForSecret),
			builder.WithPredicates(predicate.LabelChangedPredicate{}),
		).
		Named("dojo").
		Complete(r)
}
