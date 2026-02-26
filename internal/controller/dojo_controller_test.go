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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
	k8scorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "github.com/DanijelRadakovic/dojo-operator/api/v1"
)

var _ = Describe("Dojo Controller", func() {
	Context("When reconciling a resource", func() {
		const DojoName = "dojo"
		const SecretName = "dojo-app"
		const NamespacePrefix = "dojo-test"
		var namespace string

		ctx := context.Background()

		SetDefaultEventuallyTimeout(10 * time.Second)
		SetDefaultEventuallyPollingInterval(1 * time.Second)

		BeforeEach(func() {
			By("Creating the Namespace to perform the tests")
			namespace = NamespacePrefix + fmt.Sprintf("%d", time.Now().UnixNano())

			typeNamespacedName := types.NamespacedName{
				Name:      DojoName,
				Namespace: namespace,
			}

			ns := &k8scorev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: namespace},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())

			By("Creating the CNPG Secret for database credentials")
			secret := &k8scorev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      SecretName,
					Namespace: namespace,
					Labels: map[string]string{
						"cnpg.io/cluster": "dojo",
					},
				},
				Type: k8scorev1.SecretTypeBasicAuth,
				Data: map[string][]byte{
					"username": []byte("dojo"),
					"password": []byte("password"),
					"dbname":   []byte("dojo"),
					"host":     []byte("dojo-rw"),
					"port":     []byte("5432"),
					"user":     []byte("dojo"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating the custom resource for the Kind Dojo")
			if err := k8sClient.Get(ctx, typeNamespacedName, &corev1.Dojo{}); err != nil && errors.IsNotFound(err) {
				resource := &corev1.Dojo{
					ObjectMeta: metav1.ObjectMeta{
						Name:      DojoName,
						Namespace: namespace,
					},
					Spec: corev1.DojoSpec{
						AccountId:      "The Higher-Ups",
						Title:          "Tokyo Jujutsu High",
						Database:       ptr.To(corev1.DatabasePostgres),
						Replicas:       ptr.To(int32(2)),
						CredentialsRef: k8scorev1.SecretReference{Name: SecretName, Namespace: namespace},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &corev1.Dojo{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: DojoName}, resource)).To(Succeed())

			By("Cleanup the specific resource instance Dojo")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			secret := &k8scorev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: SecretName}, secret)).To(Succeed())

			By("Cleanup the credential secret")
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())

			By("Cleanup the Namespace")
			_ = k8sClient.Delete(ctx, &k8scorev1.Namespace{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}})
		})

		It("Should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &DojoReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			typeNamespacedName := types.NamespacedName{
				Name:      DojoName,
				Namespace: namespace,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking if the custom resource was successfully reconciled")

			found := &corev1.Dojo{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, found)).To(Succeed())
			Expect(found.Namespace).To(Equal(namespace))
			Expect(found.Name).To(Equal(DojoName))
			Expect(found.Status.Conditions).To(ContainElement(SatisfyAll(
				HaveField("Type", Equal(ConditionProgressingDojo)),
				HaveField("Status", Equal(metav1.ConditionTrue)),
				HaveField("Reason", Equal(ReasonInitializing)),
			)))
		})

		It("Should successfully reconcile the resource twice", func() {
			By("Reconciling the created resource")
			controllerReconciler := &DojoReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			typeNamespacedName := types.NamespacedName{
				Name:      DojoName,
				Namespace: namespace,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Checking if the custom resource was successfully reconciled")

			found := &corev1.Dojo{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, found)).To(Succeed())
			Expect(found.Namespace).To(Equal(namespace))
			Expect(found.Name).To(Equal(DojoName))
			Expect(found.Status.Credentials.Name).To(Equal(DojoName + "-credentials"))
			Expect(found.Status.Conditions).To(ContainElements(
				SatisfyAll(
					HaveField("Type", Equal(ConditionProgressingDojo)),
					HaveField("Status", Equal(metav1.ConditionTrue)),
					HaveField("Reason", Equal(ReasonCreating)),
				),
				SatisfyAll(
					HaveField("Type", Equal(ConditionDegradedDojo)),
					HaveField("Status", Equal(metav1.ConditionFalse)),
					HaveField("Reason", Equal(ReasonCreating)),
				),
			))

			secret := &k8scorev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: DojoName + "-credentials"}, secret)).To(Succeed())
			Expect(secret.OwnerReferences).To(ContainElement(SatisfyAll(
				HaveField("UID", Equal(found.UID)),
				HaveField("Controller", gstruct.PointTo(BeTrue())),
			)), "The secret should be owned by the Dojo object")

		})
	})
})
