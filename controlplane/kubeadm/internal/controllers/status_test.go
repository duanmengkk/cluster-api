/*
Copyright 2020 The Kubernetes Authors.

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

package controllers

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bootstrapv1 "sigs.k8s.io/cluster-api/api/bootstrap/kubeadm/v1beta2"
	controlplanev1 "sigs.k8s.io/cluster-api/api/controlplane/kubeadm/v1beta2"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal"
	"sigs.k8s.io/cluster-api/controlplane/kubeadm/internal/etcd"
	controlplanev1webhooks "sigs.k8s.io/cluster-api/controlplane/kubeadm/internal/webhooks"
	"sigs.k8s.io/cluster-api/util/collections"
	"sigs.k8s.io/cluster-api/util/conditions"
	v1beta1conditions "sigs.k8s.io/cluster-api/util/conditions/deprecated/v1beta1"
)

func TestKubeadmControlPlaneReconciler_setControlPlaneInitialized(t *testing.T) {
	t.Run("ControlPlaneInitialized false if the kubeadm config does not exist yet", func(t *testing.T) {
		g := NewWithT(t)
		controlPlane := &internal.ControlPlane{
			Cluster: &clusterv1.Cluster{},
			KCP:     &controlplanev1.KubeadmControlPlane{},
		}
		controlPlane.InjectTestManagementCluster(&fakeManagementCluster{
			Workload: &fakeWorkloadCluster{
				Status: internal.ClusterStatus{
					HasKubeadmConfig: false,
				},
			},
		})

		err := setControlPlaneInitialized(ctx, controlPlane)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(ptr.Deref(controlPlane.KCP.Status.Initialization.ControlPlaneInitialized, false)).To(BeFalse())

		setInitializedCondition(ctx, controlPlane.KCP)
		c := conditions.Get(controlPlane.KCP, controlplanev1.KubeadmControlPlaneInitializedCondition)
		g.Expect(c).ToNot(BeNil())
		g.Expect(*c).To(conditions.MatchCondition(metav1.Condition{
			Type:   controlplanev1.KubeadmControlPlaneInitializedCondition,
			Status: metav1.ConditionFalse,
			Reason: controlplanev1.KubeadmControlPlaneNotInitializedReason,
		}, conditions.IgnoreLastTransitionTime(true)))
	})
	t.Run("ControlPlaneInitialized true if the kubeadm config exists", func(t *testing.T) {
		g := NewWithT(t)
		controlPlane := &internal.ControlPlane{
			Cluster: &clusterv1.Cluster{},
			KCP:     &controlplanev1.KubeadmControlPlane{},
		}
		controlPlane.InjectTestManagementCluster(&fakeManagementCluster{
			Workload: &fakeWorkloadCluster{
				Status: internal.ClusterStatus{
					HasKubeadmConfig: true,
				},
			},
		})

		err := setControlPlaneInitialized(ctx, controlPlane)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(ptr.Deref(controlPlane.KCP.Status.Initialization.ControlPlaneInitialized, false)).To(BeTrue())

		setInitializedCondition(ctx, controlPlane.KCP)
		c := conditions.Get(controlPlane.KCP, controlplanev1.KubeadmControlPlaneInitializedCondition)
		g.Expect(c).ToNot(BeNil())
		g.Expect(*c).To(conditions.MatchCondition(metav1.Condition{
			Type:   controlplanev1.KubeadmControlPlaneInitializedCondition,
			Status: metav1.ConditionTrue,
			Reason: controlplanev1.KubeadmControlPlaneInitializedReason,
		}, conditions.IgnoreLastTransitionTime(true)))
	})
}

func TestSetReplicas(t *testing.T) {
	g := NewWithT(t)
	readyTrue := metav1.Condition{Type: clusterv1.MachineReadyCondition, Status: metav1.ConditionTrue}
	readyFalse := metav1.Condition{Type: clusterv1.MachineReadyCondition, Status: metav1.ConditionFalse}
	readyUnknown := metav1.Condition{Type: clusterv1.MachineReadyCondition, Status: metav1.ConditionUnknown}

	availableTrue := metav1.Condition{Type: clusterv1.MachineAvailableCondition, Status: metav1.ConditionTrue}
	availableFalse := metav1.Condition{Type: clusterv1.MachineAvailableCondition, Status: metav1.ConditionFalse}
	availableUnknown := metav1.Condition{Type: clusterv1.MachineAvailableCondition, Status: metav1.ConditionUnknown}

	upToDateTrue := metav1.Condition{Type: clusterv1.MachineUpToDateCondition, Status: metav1.ConditionTrue}
	upToDateFalse := metav1.Condition{Type: clusterv1.MachineUpToDateCondition, Status: metav1.ConditionFalse}
	upToDateUnknown := metav1.Condition{Type: clusterv1.MachineUpToDateCondition, Status: metav1.ConditionUnknown}

	kcp := &controlplanev1.KubeadmControlPlane{}
	c := &internal.ControlPlane{
		KCP: kcp,
		Machines: collections.FromMachines(
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{readyTrue, availableTrue, upToDateTrue}}},
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{readyTrue, availableTrue, upToDateTrue}}},
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{readyFalse, availableFalse, upToDateTrue}}},
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m4"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{readyTrue, availableFalse, upToDateTrue}}},
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m5"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{readyFalse, availableFalse, upToDateFalse}}},
			&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m6"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{readyUnknown, availableUnknown, upToDateUnknown}}},
		),
	}

	setReplicas(ctx, c.KCP, c.Machines)

	g.Expect(kcp.Status).ToNot(BeNil())
	g.Expect(kcp.Status.Replicas).ToNot(BeNil())
	g.Expect(*kcp.Status.Replicas).To(Equal(int32(6)))
	g.Expect(kcp.Status.ReadyReplicas).ToNot(BeNil())
	g.Expect(*kcp.Status.ReadyReplicas).To(Equal(int32(3)))
	g.Expect(kcp.Status.AvailableReplicas).ToNot(BeNil())
	g.Expect(*kcp.Status.AvailableReplicas).To(Equal(int32(2)))
	g.Expect(kcp.Status.UpToDateReplicas).ToNot(BeNil())
	g.Expect(*kcp.Status.UpToDateReplicas).To(Equal(int32(4)))
}

func Test_setInitializedCondition(t *testing.T) {
	tests := []struct {
		name            string
		controlPlane    *internal.ControlPlane
		expectCondition metav1.Condition
	}{
		{
			name: "KCP not initialized",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneInitializedCondition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotInitializedReason,
			},
		},
		{
			name: "KCP initialized",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
					},
				},
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneInitializedCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneInitializedReason,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setInitializedCondition(ctx, tt.controlPlane.KCP)

			condition := conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneInitializedCondition)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(*condition).To(conditions.MatchCondition(tt.expectCondition, conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func Test_setRollingOutCondition(t *testing.T) {
	upToDateCondition := metav1.Condition{
		Type:   clusterv1.MachineUpToDateCondition,
		Status: metav1.ConditionTrue,
		Reason: clusterv1.MachineUpToDateReason,
	}

	tests := []struct {
		name            string
		kcp             *controlplanev1.KubeadmControlPlane
		machines        []*clusterv1.Machine
		expectCondition metav1.Condition
	}{
		{
			name:     "no machines",
			kcp:      &controlplanev1.KubeadmControlPlane{},
			machines: []*clusterv1.Machine{},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneRollingOutCondition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotRollingOutReason,
			},
		},
		{
			name: "all machines are up to date",
			kcp:  &controlplanev1.KubeadmControlPlane{},
			machines: []*clusterv1.Machine{
				{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{upToDateCondition}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{upToDateCondition}}},
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneRollingOutCondition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotRollingOutReason,
			},
		},
		{
			name: "one up-to-date, two not up-to-date, one reporting up-to-date unknown",
			kcp:  &controlplanev1.KubeadmControlPlane{},
			machines: []*clusterv1.Machine{
				{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{upToDateCondition}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{
					{
						Type:   clusterv1.MachineUpToDateCondition,
						Status: metav1.ConditionUnknown,
						Reason: clusterv1.InternalErrorReason,
					},
				}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "m4"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{
					{
						Type:   clusterv1.MachineUpToDateCondition,
						Status: metav1.ConditionFalse,
						Reason: clusterv1.MachineNotUpToDateReason,
						Message: "* Failure domain failure-domain1, failure-domain2 required\n" +
							"* InfrastructureMachine is not up-to-date",
					},
				}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{
					{
						Type:    clusterv1.MachineUpToDateCondition,
						Status:  metav1.ConditionFalse,
						Reason:  clusterv1.MachineNotUpToDateReason,
						Message: "* Version v1.25.0, v1.26.0 required",
					},
				}}},
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneRollingOutCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneRollingOutReason,
				Message: "Rolling out 2 not up-to-date replicas\n" +
					"* Version v1.25.0, v1.26.0 required\n" +
					"* Failure domain failure-domain1, failure-domain2 required\n" +
					"* InfrastructureMachine is not up-to-date",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			var machines collections.Machines
			if tt.machines != nil {
				machines = collections.FromMachines(tt.machines...)
			}
			setRollingOutCondition(ctx, tt.kcp, machines)

			condition := conditions.Get(tt.kcp, controlplanev1.KubeadmControlPlaneRollingOutCondition)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(*condition).To(conditions.MatchCondition(tt.expectCondition, conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func Test_setScalingUpCondition(t *testing.T) {
	tests := []struct {
		name            string
		controlPlane    *internal.ControlPlane
		expectCondition metav1.Condition
	}{
		{
			name: "Replica not set",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingUpCondition,
				Status:  metav1.ConditionUnknown,
				Reason:  controlplanev1.KubeadmControlPlaneScalingUpWaitingForReplicasSetReason,
				Message: "Waiting for spec.replicas set",
			},
		},
		{
			name: "Not scaling up",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(3))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(3))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingUpCondition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotScalingUpReason,
			},
		},
		{
			name: "Not scaling up, infra template not found",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						Replicas: ptr.To(int32(3)),
						MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
							Spec: controlplanev1.KubeadmControlPlaneMachineTemplateSpec{
								InfrastructureRef: clusterv1.ContractVersionedObjectReference{
									Kind: "AWSTemplate",
								},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(3))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
				InfraMachineTemplateIsNotFound: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingUpCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotScalingUpReason,
				Message: "Scaling up would be blocked because AWSTemplate does not exist",
			},
		},
		{
			name: "Scaling up",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(5))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(3))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingUpCondition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneScalingUpReason,
				Message: "Scaling up from 3 to 5 replicas",
			},
		},
		{
			name: "Scaling up is always false when kcp is deleted",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: ptr.To(metav1.Time{Time: time.Now()})},
					Spec:       controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(5))},
					Status:     controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(3))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingUpCondition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotScalingUpReason,
			},
		},
		{
			name: "Scaling up, infra template not found",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						Replicas: ptr.To(int32(5)),
						MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
							Spec: controlplanev1.KubeadmControlPlaneMachineTemplateSpec{
								InfrastructureRef: clusterv1.ContractVersionedObjectReference{
									Kind: "AWSTemplate",
								},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(3))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
				InfraMachineTemplateIsNotFound: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingUpCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneScalingUpReason,
				Message: "Scaling up from 3 to 5 replicas is blocked because:\n" +
					"* AWSTemplate does not exist",
			},
		},
		{
			name: "Scaling up, preflight checks blocking",
			controlPlane: &internal.ControlPlane{
				Cluster: &clusterv1.Cluster{
					Spec: clusterv1.ClusterSpec{
						Topology: clusterv1.Topology{
							Version: "v1.32.0",
						},
					},
				},
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(5))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(3))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
				PreflightCheckResults: internal.PreflightCheckResults{
					HasDeletingMachine:               true,
					ControlPlaneComponentsNotHealthy: true,
					EtcdClusterNotHealthy:            true,
					TopologyVersionMismatch:          true,
				},
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingUpCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneScalingUpReason,
				Message: "Scaling up from 3 to 5 replicas is blocked because:\n" +
					"* waiting for a version upgrade to v1.32.0 to be propagated from Cluster.spec.topology\n" +
					"* waiting for a control plane Machine to complete deletion\n" +
					"* waiting for control plane components to become healthy\n" +
					"* waiting for etcd cluster to become healthy",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setScalingUpCondition(ctx, tt.controlPlane.Cluster, tt.controlPlane.KCP, tt.controlPlane.Machines, tt.controlPlane.InfraMachineTemplateIsNotFound, tt.controlPlane.PreflightCheckResults)

			condition := conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneScalingUpCondition)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(*condition).To(conditions.MatchCondition(tt.expectCondition, conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func Test_setScalingDownCondition(t *testing.T) {
	tests := []struct {
		name            string
		controlPlane    *internal.ControlPlane
		expectCondition metav1.Condition
	}{
		{
			name: "Replica not set",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingDownCondition,
				Status:  metav1.ConditionUnknown,
				Reason:  controlplanev1.KubeadmControlPlaneScalingDownWaitingForReplicasSetReason,
				Message: "Waiting for spec.replicas set",
			},
		},
		{
			name: "Not scaling down",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(3))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(3))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingDownCondition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotScalingDownReason,
			},
		},
		{
			name: "Scaling down",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(3))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(5))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m4"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m5"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingDownCondition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneScalingDownReason,
				Message: "Scaling down from 5 to 3 replicas",
			},
		},
		{
			name: "Scaling down to zero when kcp is deleted",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: ptr.To(metav1.Time{Time: time.Now()})},
					Spec:       controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(3))},
					Status:     controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(5))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m4"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m5"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneScalingDownCondition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneScalingDownReason,
				Message: "Scaling down from 5 to 0 replicas",
			},
		},
		{
			name: "Scaling down with one stale machine",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(1))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(3))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1", DeletionTimestamp: ptr.To(metav1.Time{Time: time.Now().Add(-1 * time.Hour)})},
						Status: clusterv1.MachineStatus{
							Conditions: []metav1.Condition{
								{
									Type:   clusterv1.MachineDeletingCondition,
									Status: metav1.ConditionTrue,
									Reason: clusterv1.MachineDeletingDrainingNodeReason,
									Message: `Drain not completed yet (started at 2024-10-09T16:13:59Z):
* Pods pod-2-deletionTimestamp-set-1, pod-3-to-trigger-eviction-successfully-1: deletionTimestamp set, but still not removed from the Node
* Pod pod-5-to-trigger-eviction-pdb-violated-1: cannot evict pod as it would violate the pod's disruption budget. The disruption budget pod-5-pdb needs 20 healthy pods and has 20 currently
* Pod pod-6-to-trigger-eviction-some-other-error: failed to evict Pod, some other error 1
* Pod pod-9-wait-completed: waiting for completion
After above Pods have been removed from the Node, the following Pods will be evicted: pod-7-eviction-later, pod-8-eviction-later`,
								},
							},
							Deletion: &clusterv1.MachineDeletionStatus{
								NodeDrainStartTime: metav1.Time{Time: time.Now().Add(-6 * time.Minute)},
							},
						},
					},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingDownCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneScalingDownReason,
				Message: "Scaling down from 3 to 1 replicas is blocked because:\n" +
					"* Machine m1 is in deletion since more than 15m, delay likely due to PodDisruptionBudgets, Pods not terminating, Pod eviction errors, Pods not completed yet",
			},
		},
		{
			name: "Scaling down with two stale machine",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(1))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(3))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1", DeletionTimestamp: ptr.To(metav1.Time{Time: time.Now().Add(-1 * time.Hour)})}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2", DeletionTimestamp: ptr.To(metav1.Time{Time: time.Now().Add(-1 * time.Hour)})}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingDownCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneScalingDownReason,
				Message: "Scaling down from 3 to 1 replicas is blocked because:\n" +
					"* Machines m1, m2 are in deletion since more than 15m",
			},
		},
		{
			name: "Scaling down, preflight checks blocking",
			controlPlane: &internal.ControlPlane{
				Cluster: &clusterv1.Cluster{
					Spec: clusterv1.ClusterSpec{
						Topology: clusterv1.Topology{
							Version: "v1.32.0",
						},
					},
				},
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec:   controlplanev1.KubeadmControlPlaneSpec{Replicas: ptr.To(int32(1))},
					Status: controlplanev1.KubeadmControlPlaneStatus{Replicas: ptr.To(int32(3))},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}},
				),
				PreflightCheckResults: internal.PreflightCheckResults{
					HasDeletingMachine:               true,
					ControlPlaneComponentsNotHealthy: true,
					EtcdClusterNotHealthy:            true,
					TopologyVersionMismatch:          true,
				},
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneScalingDownCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneScalingDownReason,
				Message: "Scaling down from 3 to 1 replicas is blocked because:\n" +
					"* waiting for a version upgrade to v1.32.0 to be propagated from Cluster.spec.topology\n" +
					"* waiting for a control plane Machine to complete deletion\n" +
					"* waiting for control plane components to become healthy\n" +
					"* waiting for etcd cluster to become healthy",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setScalingDownCondition(ctx, tt.controlPlane.Cluster, tt.controlPlane.KCP, tt.controlPlane.Machines, tt.controlPlane.PreflightCheckResults)

			condition := conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneScalingDownCondition)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(*condition).To(conditions.MatchCondition(tt.expectCondition, conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func Test_setMachinesReadyAndMachinesUpToDateConditions(t *testing.T) {
	readyTrue := metav1.Condition{Type: clusterv1.MachineReadyCondition, Status: metav1.ConditionTrue, Reason: clusterv1.MachineReadyReason}
	readyFalse := metav1.Condition{Type: clusterv1.MachineReadyCondition, Status: metav1.ConditionFalse, Reason: clusterv1.MachineNotReadyReason, Message: "NotReady"}

	upToDateTrue := metav1.Condition{Type: clusterv1.MachineUpToDateCondition, Status: metav1.ConditionTrue, Reason: clusterv1.MachineUpToDateReason}
	upToDateFalse := metav1.Condition{Type: clusterv1.MachineUpToDateCondition, Status: metav1.ConditionFalse, Reason: clusterv1.MachineNotUpToDateReason, Message: "NotUpToDate"}

	tests := []struct {
		name                            string
		controlPlane                    *internal.ControlPlane
		expectMachinesReadyCondition    metav1.Condition
		expectMachinesUpToDateCondition metav1.Condition
	}{
		{
			name: "Without machines",
			controlPlane: &internal.ControlPlane{
				KCP:      &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(),
			},
			expectMachinesReadyCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneMachinesReadyCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneMachinesReadyNoReplicasReason,
			},
			expectMachinesUpToDateCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneMachinesUpToDateCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneMachinesUpToDateNoReplicasReason,
			},
		},
		{
			name: "With machines",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{readyTrue, upToDateTrue}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{readyTrue, upToDateFalse}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{readyFalse, upToDateFalse}}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m4"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{readyFalse}}},                                                                         // Machine without UpToDate condition
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m5", CreationTimestamp: metav1.Time{Time: time.Now().Add(-5 * time.Second)}}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{readyFalse}}}, // New Machine without UpToDate condition (should be ignored)
				),
			},
			expectMachinesReadyCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneMachinesReadyCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneMachinesNotReadyReason,
				Message: "* Machines m3, m4, m5: NotReady",
			},
			expectMachinesUpToDateCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneMachinesUpToDateCondition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneMachinesNotUpToDateReason,
				Message: "* Machines m2, m3: NotUpToDate\n" +
					"* Machine m4: Condition UpToDate not yet reported",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setMachinesReadyCondition(ctx, tt.controlPlane.KCP, tt.controlPlane.Machines)
			setMachinesUpToDateCondition(ctx, tt.controlPlane.KCP, tt.controlPlane.Machines)

			readyCondition := conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneMachinesReadyCondition)
			g.Expect(readyCondition).ToNot(BeNil())
			g.Expect(*readyCondition).To(conditions.MatchCondition(tt.expectMachinesReadyCondition, conditions.IgnoreLastTransitionTime(true)))

			upToDateCondition := conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneMachinesUpToDateCondition)
			g.Expect(upToDateCondition).ToNot(BeNil())
			g.Expect(*upToDateCondition).To(conditions.MatchCondition(tt.expectMachinesUpToDateCondition, conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func Test_setRemediatingCondition(t *testing.T) {
	healthCheckSucceeded := metav1.Condition{Type: clusterv1.MachineHealthCheckSucceededCondition, Status: metav1.ConditionTrue}
	healthCheckNotSucceeded := metav1.Condition{Type: clusterv1.MachineHealthCheckSucceededCondition, Status: metav1.ConditionFalse}
	ownerRemediated := metav1.Condition{Type: clusterv1.MachineOwnerRemediatedCondition, Status: metav1.ConditionFalse, Reason: controlplanev1.KubeadmControlPlaneMachineRemediationMachineDeletingReason, Message: "Machine is deleting"}

	tests := []struct {
		name            string
		controlPlane    *internal.ControlPlane
		expectCondition metav1.Condition
	}{
		{
			name: "Without unhealthy machines",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}},
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}},
				),
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneRemediatingCondition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotRemediatingReason,
			},
		},
		{
			name: "With machines to be remediated by KCP",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{healthCheckSucceeded}}},    // Healthy machine
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{healthCheckNotSucceeded}}}, // Unhealthy machine, not yet marked for remediation
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{healthCheckNotSucceeded, ownerRemediated}}},
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneRemediatingCondition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneRemediatingReason,
				Message: "* Machine m3: Machine is deleting",
			},
		},
		{
			name: "With one unhealthy machine not to be remediated by KCP",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{healthCheckSucceeded}}},    // Healthy machine
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{healthCheckNotSucceeded}}}, // Unhealthy machine, not yet marked for remediation
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{healthCheckSucceeded}}},    // Healthy machine
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneRemediatingCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotRemediatingReason,
				Message: "Machine m2 is not healthy (not to be remediated by KubeadmControlPlane)",
			},
		},
		{
			name: "With two unhealthy machine not to be remediated by KCP",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{},
				Machines: collections.FromMachines(
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{healthCheckNotSucceeded}}}, // Unhealthy machine, not yet marked for remediation
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m2"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{healthCheckNotSucceeded}}}, // Unhealthy machine, not yet marked for remediation
					&clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m3"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{healthCheckSucceeded}}},    // Healthy machine
				),
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneRemediatingCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotRemediatingReason,
				Message: "Machines m1, m2 are not healthy (not to be remediated by KubeadmControlPlane)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setRemediatingCondition(ctx, tt.controlPlane.KCP, tt.controlPlane.MachinesToBeRemediatedByKCP(), tt.controlPlane.UnhealthyMachines())

			condition := conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneRemediatingCondition)
			g.Expect(condition).ToNot(BeNil())
			g.Expect(*condition).To(conditions.MatchCondition(tt.expectCondition, conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func TestDeletingCondition(t *testing.T) {
	testCases := []struct {
		name            string
		kcp             *controlplanev1.KubeadmControlPlane
		deletingReason  string
		deletingMessage string
		expectCondition metav1.Condition
	}{
		{
			name: "deletionTimestamp not set",
			kcp: &controlplanev1.KubeadmControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kcp-test",
					Namespace: metav1.NamespaceDefault,
				},
			},
			deletingReason:  "",
			deletingMessage: "",
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneDeletingCondition,
				Status: metav1.ConditionFalse,
				Reason: controlplanev1.KubeadmControlPlaneNotDeletingReason,
			},
		},
		{
			name: "deletionTimestamp set (waiting for control plane Machine deletion)",
			kcp: &controlplanev1.KubeadmControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "kcp-test",
					Namespace:         metav1.NamespaceDefault,
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
			},
			deletingReason:  controlplanev1.KubeadmControlPlaneDeletingWaitingForMachineDeletionReason,
			deletingMessage: "Deleting 3 Machines",
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneDeletingCondition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneDeletingWaitingForMachineDeletionReason,
				Message: "Deleting 3 Machines",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			setDeletingCondition(ctx, tc.kcp, tc.deletingReason, tc.deletingMessage)

			deletingCondition := conditions.Get(tc.kcp, controlplanev1.KubeadmControlPlaneDeletingCondition)
			g.Expect(deletingCondition).ToNot(BeNil())
			g.Expect(*deletingCondition).To(conditions.MatchCondition(tc.expectCondition, conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func Test_shouldSurfaceWhenAvailableTrue(t *testing.T) {
	reconcileTime := time.Now()

	apiServerPodHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyCondition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	apiServerPodNotHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyCondition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	apiServerPodNotHealthy11s := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyCondition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime.Add(-11 * time.Second)}}

	etcdMemberHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyCondition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	etcdMemberNotHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyCondition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime}}

	testCases := []struct {
		name    string
		machine *clusterv1.Machine
		want    bool
	}{
		{
			name:    "Machine doesn't have issues, it should not surface",
			machine: &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodHealthy, etcdMemberHealthy}}},
			want:    false,
		},
		{
			name:    "Machine has issue set by less than 10s it should not surface",
			machine: &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodNotHealthy, etcdMemberNotHealthy}}},
			want:    false,
		},
		{
			name:    "Machine has at least one issue set by more than 10s it should surface",
			machine: &clusterv1.Machine{ObjectMeta: metav1.ObjectMeta{Name: "m1"}, Status: clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodNotHealthy11s, etcdMemberNotHealthy}}},
			want:    true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			got := shouldSurfaceWhenAvailableTrue(tc.machine, controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyCondition, controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyCondition)
			g.Expect(got).To(Equal(tc.want))
		})
	}
}

func Test_setAvailableCondition(t *testing.T) {
	reconcileTime := time.Now()

	certificatesReady := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneCertificatesAvailableCondition, Status: metav1.ConditionTrue}
	certificatesNotReady := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneCertificatesAvailableCondition, Status: metav1.ConditionFalse}

	apiServerPodHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyCondition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	apiServerPodNotHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyCondition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	apiServerPodNotHealthy11s := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyCondition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime.Add(-11 * time.Second)}}
	controllerManagerPodHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineControllerManagerPodHealthyCondition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	schedulerPodHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineSchedulerPodHealthyCondition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	etcdPodHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdPodHealthyCondition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}

	etcdMemberHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyCondition, Status: metav1.ConditionTrue, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	etcdMemberNotHealthy := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyCondition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	etcdMemberNotHealthy11s := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyCondition, Status: metav1.ConditionFalse, LastTransitionTime: metav1.Time{Time: reconcileTime.Add(-11 * time.Second)}}

	apiServerPodHealthyUnknown := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineAPIServerPodHealthyCondition, Status: metav1.ConditionUnknown, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	controllerManagerPodHealthyUnknown := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineControllerManagerPodHealthyCondition, Status: metav1.ConditionUnknown, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	schedulerPodHealthyUnknown := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineSchedulerPodHealthyCondition, Status: metav1.ConditionUnknown, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	etcdPodHealthyUnknown := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdPodHealthyCondition, Status: metav1.ConditionUnknown, LastTransitionTime: metav1.Time{Time: reconcileTime}}
	etcdMemberHealthyUnknown11s := metav1.Condition{Type: controlplanev1.KubeadmControlPlaneMachineEtcdMemberHealthyCondition, Status: metav1.ConditionUnknown, LastTransitionTime: metav1.Time{Time: reconcileTime.Add(-11 * time.Second)}}

	tests := []struct {
		name            string
		controlPlane    *internal.ControlPlane
		expectCondition metav1.Condition
	}{

		// Not initialized

		{
			name: "KCP is not available, not yet initialized",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{Local: bootstrapv1.LocalEtcd{}},
							},
						},
					},
				},
				EtcdMembers: []*etcd.Member{},
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableReason,
				Message: "Control plane not yet initialized",
			},
		},

		// Available (all good)

		{
			name: "KCP is available (1 CP)",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableReason,
			},
		},
		{
			name: "KCP is available (3 CP)",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m3"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
					{Name: "m2", IsLearner: false},
					{Name: "m3", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableReason,
			},
		},

		// With not healthy etcd members / other etcd failures

		{
			name: "KCP is not available, failed to get etcd members right after being initialized",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{Local: bootstrapv1.LocalEtcd{}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{
							{Type: controlplanev1.KubeadmControlPlaneInitializedCondition, Status: metav1.ConditionTrue, Reason: controlplanev1.KubeadmControlPlaneInitializedReason, LastTransitionTime: metav1.Time{Time: reconcileTime.Add(-5 * time.Second)}},
						},
					},
				},
				EtcdMembers: nil,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableReason,
				Message: "Waiting for etcd to report the list of members",
			},
		},
		{
			name: "KCP is not available, failed to get etcd members, 2m after the cluster was initialized",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{Local: bootstrapv1.LocalEtcd{}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{
							{Type: controlplanev1.KubeadmControlPlaneInitializedCondition, Status: metav1.ConditionTrue, Reason: controlplanev1.KubeadmControlPlaneInitializedReason, LastTransitionTime: metav1.Time{Time: reconcileTime.Add(-5 * time.Minute)}},
						},
					},
				},
				EtcdMembers: nil,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionUnknown,
				Reason:  controlplanev1.KubeadmControlPlaneAvailableInspectionFailedReason,
				Message: "Failed to get etcd members",
			},
		},
		{
			name: "KCP is not available, etcd members and machines list do not match",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{Local: bootstrapv1.LocalEtcd{}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
					},
				},
				EtcdMembers:                       []*etcd.Member{},
				EtcdMembersAndMachinesAreMatching: false,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableReason,
				Message: "The list of etcd members does not match the list of Machines and Nodes",
			},
		},

		{
			name: "KCP is available, one not healthy etcd member, but within quorum (not reported)",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m3"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
					{Name: "m2", IsLearner: false},
					{Name: "m3", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableReason,
			},
		},
		{
			name: "KCP is available, one not healthy etcd member, but within quorum (reported)",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m3"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy11s},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
					{Name: "m2", IsLearner: false},
					{Name: "m3", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableReason,
				Message: "* 2 of 3 etcd members are healthy, at least 2 healthy member required for etcd quorum\n" +
					"* 2 of 3 Machines have healthy control plane components, at least 1 required", // Note, when an etcd member is not healthy, also the corresponding CP is considered not healthy.
			},
		},
		{
			name: "KCP is not available, Not enough healthy etcd members",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m3"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
					{Name: "m2", IsLearner: false},
					{Name: "m3", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableReason,
				Message: "* 1 of 3 etcd members is healthy, at least 2 healthy member required for etcd quorum",
			},
		},
		{
			name: "KCP is available, machines without provider ID are ignored",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodHealthyUnknown, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneAvailableReason,
				Message: "",
			},
		},
		{
			name: "KCP is available, etcd members without name are considered healthy and not voting",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m3"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy11s},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m4"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m4"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m4"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy11s},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
					{Name: "m2", IsLearner: false},
					{Name: "m3", IsLearner: false},
					{Name: "", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableReason,
				Message: "* 3 of 4 etcd members are healthy, 1 learner etcd member, at least 2 healthy member required for etcd quorum\n" + // m4 is considered learner, so we have 3 voting members, quorum 2
					"* 2 of 4 Machines have healthy control plane components, at least 1 required",
			},
		},
		{
			name: "KCP is available, etcd members without a machine are bound to provisioning machines (focus on binding)",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m3"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy11s},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m4"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m4"},
						Status: clusterv1.MachineStatus{
							// NodeRef is not set
							// Note this is not a real use case, but it helps to validate that machine m4 is bound to an etcd member and counted as healthy.
							// If instead we use unknown or false conditions, it would not be possible to understand if the best effort binding happened or the etcd member was considered unhealthy because without a machine match.
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
					{Name: "m2", IsLearner: false},
					{Name: "m3", IsLearner: false},
					{Name: "m4", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableReason,
				Message: "* 3 of 4 etcd members are healthy, at least 3 healthy member required for etcd quorum\n" + // member m4 is linked to machine m4 eve if it doesn't have a node yet
					"* 3 of 4 Machines have healthy control plane components, at least 1 required",
			},
		},
		{
			name: "KCP is available, etcd members without a machine are bound to provisioning machines",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m3"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m4"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m4"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{},
							Conditions: []metav1.Condition{apiServerPodHealthyUnknown, controllerManagerPodHealthyUnknown, schedulerPodHealthyUnknown, etcdPodHealthyUnknown, etcdMemberHealthyUnknown11s},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
					{Name: "m2", IsLearner: false},
					{Name: "m3", IsLearner: false},
					{Name: "m4", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableReason,
				Message: "* 3 of 4 etcd members are healthy, at least 3 healthy member required for etcd quorum\n" + // member m4 is linked to machine m4 eve if it doesn't have a node yet
					"* 3 of 4 Machines have healthy control plane components, at least 1 required",
			},
		},
		{
			name: "KCP is available, members without a machine are considered not healthy",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m3-does-not-exist"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
					{Name: "m2", IsLearner: false},
					{Name: "m3", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneAvailableReason,
				Message: "* 2 of 3 etcd members are healthy, at least 2 healthy member required for etcd quorum",
			},
		},
		{
			name: "KCP is available, learner etcd members are not considered for quorum",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m3"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy11s},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m4"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m4"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m4"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberNotHealthy11s},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
					{Name: "m2", IsLearner: false},
					{Name: "m3", IsLearner: false},
					{Name: "m4", IsLearner: true},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:   controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status: metav1.ConditionTrue,
				Reason: controlplanev1.KubeadmControlPlaneAvailableReason,
				Message: "* 2 of 4 etcd members are healthy, 1 learner etcd member, at least 2 healthy member required for etcd quorum\n" + // m4 is learner, so we have 3 voting members, quorum 2
					"* 2 of 4 Machines have healthy control plane components, at least 1 required",
			},
		},

		// With not healthy K8s control planes

		{
			name: "KCP is available, machines without provider ID are ignored",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodHealthyUnknown, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneAvailableReason,
				Message: "",
			},
		},
		{
			name: "KCP is available, but with not healthy K8s control planes (one to be reported, one not yet)",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m2"},
							Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m3"},
							Conditions: []metav1.Condition{apiServerPodNotHealthy11s, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
					{Name: "m2", IsLearner: false},
					{Name: "m3", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneAvailableReason,
				Message: "* 2 of 3 Machines have healthy control plane components, at least 1 required", // two are not healthy, but one just flipped recently and 10s safeguard against flake did not expired yet
			},
		},
		{
			name: "KCP is not available, not enough healthy K8s control planes",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status:     clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status:     clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status:     clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy}},
					},
				),
				EtcdMembers:                       []*etcd.Member{{}, {}, {}},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableReason,
				Message: "* There are no Machines with healthy control plane components, at least 1 required",
			},
		},

		// With external etcd

		{
			name: "KCP is available, but with not healthy K8s control planes (one to be reported, one not yet) (external etcd)",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{External: bootstrapv1.ExternalEtcd{
									Endpoints: []string{"1.2.3.4"},
								}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status:     clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy}},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status:     clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy}},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status:     clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodNotHealthy11s, controllerManagerPodHealthy, schedulerPodHealthy}},
					},
				),
				EtcdMembers:                       nil,
				EtcdMembersAndMachinesAreMatching: false,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionTrue,
				Reason:  controlplanev1.KubeadmControlPlaneAvailableReason,
				Message: "* 2 of 3 Machines have healthy control plane components, at least 1 required", // two are not healthy, but one just flipped recently and 10s safeguard against flake did not expired yet
			},
		},
		{
			name: "KCP is not available, not enough healthy K8s control planes (external etcd)",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Spec: controlplanev1.KubeadmControlPlaneSpec{
						KubeadmConfigSpec: bootstrapv1.KubeadmConfigSpec{
							ClusterConfiguration: bootstrapv1.ClusterConfiguration{
								Etcd: bootstrapv1.Etcd{External: bootstrapv1.ExternalEtcd{
									Endpoints: []string{"1.2.3.4"},
								}},
							},
						},
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status:     clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy}},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m2"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m2"},
						Status:     clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy}},
					},
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m3"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m3"},
						Status:     clusterv1.MachineStatus{Conditions: []metav1.Condition{apiServerPodNotHealthy, controllerManagerPodHealthy, schedulerPodHealthy}},
					},
				),
				EtcdMembers:                       nil,
				EtcdMembersAndMachinesAreMatching: false,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableReason,
				Message: "* There are no Machines with healthy control plane components, at least 1 required",
			},
		},

		// With certificates not available

		{
			name: "Certificates are not available",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesNotReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
				),
				EtcdMembers: []*etcd.Member{
					{Name: "m1", IsLearner: false},
				},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableReason,
				Message: "* Control plane certificates are not available",
			},
		},

		// Deleted

		{
			name: "KCP is deleting",
			controlPlane: &internal.ControlPlane{
				KCP: &controlplanev1.KubeadmControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						DeletionTimestamp: ptr.To(metav1.Now()),
					},
					Status: controlplanev1.KubeadmControlPlaneStatus{
						Initialization: controlplanev1.KubeadmControlPlaneInitializationStatus{
							ControlPlaneInitialized: ptr.To(true),
						},
						Conditions: []metav1.Condition{certificatesReady},
					},
				},
				Machines: collections.FromMachines(
					&clusterv1.Machine{
						ObjectMeta: metav1.ObjectMeta{Name: "m1"},
						Spec:       clusterv1.MachineSpec{ProviderID: "m1"},
						Status: clusterv1.MachineStatus{
							NodeRef:    clusterv1.MachineNodeReference{Name: "m1"},
							Conditions: []metav1.Condition{apiServerPodHealthy, controllerManagerPodHealthy, schedulerPodHealthy, etcdPodHealthy, etcdMemberHealthy},
						},
					},
				),
				EtcdMembers:                       []*etcd.Member{{Name: "m1"}},
				EtcdMembersAndMachinesAreMatching: true,
			},
			expectCondition: metav1.Condition{
				Type:    controlplanev1.KubeadmControlPlaneAvailableCondition,
				Status:  metav1.ConditionFalse,
				Reason:  controlplanev1.KubeadmControlPlaneNotAvailableReason,
				Message: "* Control plane metadata.deletionTimestamp is set",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			setAvailableCondition(ctx, tt.controlPlane.KCP, tt.controlPlane.IsEtcdManaged(), tt.controlPlane.EtcdMembers, tt.controlPlane.EtcdMembersAndMachinesAreMatching, tt.controlPlane.Machines)

			availableCondition := conditions.Get(tt.controlPlane.KCP, controlplanev1.KubeadmControlPlaneAvailableCondition)
			g.Expect(availableCondition).ToNot(BeNil())
			g.Expect(*availableCondition).To(conditions.MatchCondition(tt.expectCondition, conditions.IgnoreLastTransitionTime(true)))
		})
	}
}

func TestKubeadmControlPlaneReconciler_updateStatusNoMachines(t *testing.T) {
	g := NewWithT(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: metav1.NamespaceDefault,
		},
	}

	kcp := &controlplanev1.KubeadmControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeadmControlPlane",
			APIVersion: controlplanev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      "foo",
		},
		Spec: controlplanev1.KubeadmControlPlaneSpec{
			Version: "v1.16.6",
			MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
				Spec: controlplanev1.KubeadmControlPlaneMachineTemplateSpec{
					InfrastructureRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: "test",
						Kind:     "UnknownInfraMachine",
						Name:     "foo",
					},
				},
			},
		},
	}
	webhook := &controlplanev1webhooks.KubeadmControlPlane{}
	g.Expect(webhook.Default(ctx, kcp)).To(Succeed())
	_, err := webhook.ValidateCreate(ctx, kcp)
	g.Expect(err).ToNot(HaveOccurred())

	fakeClient := newFakeClient(kcp.DeepCopy(), cluster.DeepCopy())

	r := &KubeadmControlPlaneReconciler{
		Client: fakeClient,
		managementCluster: &fakeManagementCluster{
			Machines: map[string]*clusterv1.Machine{},
			Workload: &fakeWorkloadCluster{},
		},
		recorder: record.NewFakeRecorder(32),
	}

	controlPlane := &internal.ControlPlane{
		KCP:     kcp,
		Cluster: cluster,
	}
	controlPlane.InjectTestManagementCluster(r.managementCluster)

	g.Expect(r.updateV1Beta1Status(ctx, controlPlane)).To(Succeed())
	g.Expect(kcp.Status.Deprecated.V1Beta1.ReadyReplicas).To(BeEquivalentTo(0))
	g.Expect(kcp.Status.Deprecated.V1Beta1.UnavailableReplicas).To(BeEquivalentTo(0))
	g.Expect(kcp.Status.Deprecated.V1Beta1.FailureMessage).To(BeNil())
	g.Expect(kcp.Status.Deprecated.V1Beta1.FailureReason).To(BeEquivalentTo(""))
}

func TestKubeadmControlPlaneReconciler_setLastRemediation(t *testing.T) {
	t.Run("No remediation yet", func(t *testing.T) {
		g := NewWithT(t)
		controlPlane := &internal.ControlPlane{
			KCP: &controlplanev1.KubeadmControlPlane{},
		}

		err := setLastRemediation(ctx, controlPlane)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(reflect.DeepEqual(controlPlane.KCP.Status.LastRemediation, controlplanev1.LastRemediationStatus{})).To(BeTrue())
	})
	t.Run("Remediation in progress", func(t *testing.T) {
		g := NewWithT(t)

		r1 := RemediationData{
			Machine:    "m2",
			Timestamp:  metav1.Now().Rfc3339Copy(),
			RetryCount: 2,
		}
		dr1, err := r1.Marshal()
		g.Expect(err).ToNot(HaveOccurred())

		r2 := RemediationData{
			Machine:    "m1",
			Timestamp:  metav1.Now().Rfc3339Copy(),
			RetryCount: 1,
		}
		dr2, err := r2.Marshal()
		g.Expect(err).ToNot(HaveOccurred())

		controlPlane := &internal.ControlPlane{
			KCP: &controlplanev1.KubeadmControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						// Remediation in progress in KCP should take precedence on old remediation recorded on machines.
						controlplanev1.RemediationInProgressAnnotation: dr1,
					},
				},
			},
			Machines: map[string]*clusterv1.Machine{
				"m1": {
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							controlplanev1.RemediationForAnnotation: dr2,
						},
					},
				},
			},
		}

		err = setLastRemediation(ctx, controlPlane)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(controlPlane.KCP.Status.LastRemediation.Machine).To(Equal(r1.Machine))
		g.Expect(controlPlane.KCP.Status.LastRemediation.Time.Time).To(BeTemporally("==", r1.Timestamp.Time), cmp.Diff(controlPlane.KCP.Status.LastRemediation.Time.Time, r1.Timestamp.Time))
		g.Expect(*controlPlane.KCP.Status.LastRemediation.RetryCount).To(Equal(int32(r1.RetryCount)))
	})
	t.Run("Remediation completed, get data from past remediation", func(t *testing.T) {
		g := NewWithT(t)

		r2 := RemediationData{
			Machine:    "m1",
			Timestamp:  metav1.Now().Rfc3339Copy(),
			RetryCount: 1,
		}
		dr2, err := r2.Marshal()
		g.Expect(err).ToNot(HaveOccurred())

		controlPlane := &internal.ControlPlane{
			KCP: &controlplanev1.KubeadmControlPlane{},
			Machines: map[string]*clusterv1.Machine{
				"m1": {
					ObjectMeta: metav1.ObjectMeta{
						Annotations: map[string]string{
							controlplanev1.RemediationForAnnotation: dr2,
						},
					},
				},
			},
		}

		err = setLastRemediation(ctx, controlPlane)
		g.Expect(err).ToNot(HaveOccurred())

		g.Expect(controlPlane.KCP.Status.LastRemediation.Machine).To(Equal(r2.Machine))
		g.Expect(controlPlane.KCP.Status.LastRemediation.Time.Time).To(BeTemporally("==", r2.Timestamp.Time), cmp.Diff(controlPlane.KCP.Status.LastRemediation.Time.Time, r2.Timestamp.Time))
		g.Expect(*controlPlane.KCP.Status.LastRemediation.RetryCount).To(Equal(int32(r2.RetryCount)))
	})
}

func TestKubeadmControlPlaneReconciler_updateStatusAllMachinesNotReady(t *testing.T) {
	g := NewWithT(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: metav1.NamespaceDefault,
		},
	}

	kcp := &controlplanev1.KubeadmControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeadmControlPlane",
			APIVersion: controlplanev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      "foo",
		},
		Spec: controlplanev1.KubeadmControlPlaneSpec{
			Version: "v1.16.6",
			MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
				Spec: controlplanev1.KubeadmControlPlaneMachineTemplateSpec{
					InfrastructureRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: "test",
						Kind:     "UnknownInfraMachine",
						Name:     "foo",
					},
				},
			},
		},
	}
	webhook := &controlplanev1webhooks.KubeadmControlPlane{}
	g.Expect(webhook.Default(ctx, kcp)).To(Succeed())
	_, err := webhook.ValidateCreate(ctx, kcp)
	g.Expect(err).ToNot(HaveOccurred())

	machines := map[string]*clusterv1.Machine{}
	objs := []client.Object{cluster.DeepCopy(), kcp.DeepCopy()}
	for i := range 3 {
		name := fmt.Sprintf("test-%d", i)
		m, n := createMachineNodePair(name, cluster, kcp, false)
		objs = append(objs, n, m)
		machines[m.Name] = m
	}

	fakeClient := newFakeClient(objs...)

	r := &KubeadmControlPlaneReconciler{
		Client: fakeClient,
		managementCluster: &fakeManagementCluster{
			Machines: machines,
			Workload: &fakeWorkloadCluster{},
		},
		recorder: record.NewFakeRecorder(32),
	}

	controlPlane := &internal.ControlPlane{
		KCP:      kcp,
		Cluster:  cluster,
		Machines: machines,
	}
	controlPlane.InjectTestManagementCluster(r.managementCluster)

	g.Expect(r.updateV1Beta1Status(ctx, controlPlane)).To(Succeed())
	g.Expect(kcp.Status.Deprecated.V1Beta1.ReadyReplicas).To(BeEquivalentTo(0))
	g.Expect(kcp.Status.Deprecated.V1Beta1.UnavailableReplicas).To(BeEquivalentTo(3))
	g.Expect(kcp.Status.Deprecated.V1Beta1.FailureMessage).To(BeNil())
	g.Expect(kcp.Status.Deprecated.V1Beta1.FailureReason).To(BeEquivalentTo(""))
	g.Expect(ptr.Deref(kcp.Status.Initialization.ControlPlaneInitialized, false)).To(BeFalse())
}

func TestKubeadmControlPlaneReconciler_updateStatusAllMachinesReady(t *testing.T) {
	g := NewWithT(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: metav1.NamespaceDefault,
			Name:      "foo",
		},
	}

	kcp := &controlplanev1.KubeadmControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeadmControlPlane",
			APIVersion: controlplanev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      "foo",
		},
		Spec: controlplanev1.KubeadmControlPlaneSpec{
			Version: "v1.16.6",
			MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
				Spec: controlplanev1.KubeadmControlPlaneMachineTemplateSpec{
					InfrastructureRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: "test",
						Kind:     "UnknownInfraMachine",
						Name:     "foo",
					},
				},
			},
		},
	}
	webhook := &controlplanev1webhooks.KubeadmControlPlane{}
	g.Expect(webhook.Default(ctx, kcp)).To(Succeed())
	_, err := webhook.ValidateCreate(ctx, kcp)
	g.Expect(err).ToNot(HaveOccurred())

	objs := []client.Object{cluster.DeepCopy(), kcp.DeepCopy(), kubeadmConfigMap()}
	machines := map[string]*clusterv1.Machine{}
	for i := range 3 {
		name := fmt.Sprintf("test-%d", i)
		m, n := createMachineNodePair(name, cluster, kcp, true)
		objs = append(objs, n, m)
		machines[m.Name] = m
	}

	fakeClient := newFakeClient(objs...)

	r := &KubeadmControlPlaneReconciler{
		Client: fakeClient,
		managementCluster: &fakeManagementCluster{
			Machines: machines,
			Workload: &fakeWorkloadCluster{
				Status: internal.ClusterStatus{
					Nodes:            3,
					ReadyNodes:       3,
					HasKubeadmConfig: true,
				},
			},
		},
		recorder: record.NewFakeRecorder(32),
	}

	controlPlane := &internal.ControlPlane{
		KCP:      kcp,
		Cluster:  cluster,
		Machines: machines,
	}
	controlPlane.InjectTestManagementCluster(r.managementCluster)

	g.Expect(r.updateV1Beta1Status(ctx, controlPlane)).To(Succeed())
	g.Expect(kcp.Status.Deprecated.V1Beta1.ReadyReplicas).To(BeEquivalentTo(3))
	g.Expect(kcp.Status.Deprecated.V1Beta1.UnavailableReplicas).To(BeEquivalentTo(0))
	g.Expect(kcp.Status.Deprecated.V1Beta1.FailureMessage).To(BeNil())
	g.Expect(kcp.Status.Deprecated.V1Beta1.FailureReason).To(BeEquivalentTo(""))
	g.Expect(v1beta1conditions.IsTrue(kcp, controlplanev1.AvailableV1Beta1Condition)).To(BeTrue())
	g.Expect(v1beta1conditions.IsTrue(kcp, controlplanev1.MachinesCreatedV1Beta1Condition)).To(BeTrue())
}

func TestKubeadmControlPlaneReconciler_updateStatusMachinesReadyMixed(t *testing.T) {
	g := NewWithT(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: metav1.NamespaceDefault,
		},
	}

	kcp := &controlplanev1.KubeadmControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeadmControlPlane",
			APIVersion: controlplanev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      "foo",
		},
		Spec: controlplanev1.KubeadmControlPlaneSpec{
			Version: "v1.16.6",
			MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
				Spec: controlplanev1.KubeadmControlPlaneMachineTemplateSpec{
					InfrastructureRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: "test",
						Kind:     "UnknownInfraMachine",
						Name:     "foo",
					},
				},
			},
		},
	}
	webhook := &controlplanev1webhooks.KubeadmControlPlane{}
	g.Expect(webhook.Default(ctx, kcp)).To(Succeed())
	_, err := webhook.ValidateCreate(ctx, kcp)
	g.Expect(err).ToNot(HaveOccurred())
	machines := map[string]*clusterv1.Machine{}
	objs := []client.Object{cluster.DeepCopy(), kcp.DeepCopy()}
	for i := range 4 {
		name := fmt.Sprintf("test-%d", i)
		m, n := createMachineNodePair(name, cluster, kcp, false)
		machines[m.Name] = m
		objs = append(objs, n, m)
	}
	m, n := createMachineNodePair("testReady", cluster, kcp, true)
	objs = append(objs, n, m, kubeadmConfigMap())
	machines[m.Name] = m
	fakeClient := newFakeClient(objs...)

	r := &KubeadmControlPlaneReconciler{
		Client: fakeClient,
		managementCluster: &fakeManagementCluster{
			Machines: machines,
			Workload: &fakeWorkloadCluster{
				Status: internal.ClusterStatus{
					Nodes:            5,
					ReadyNodes:       1,
					HasKubeadmConfig: true,
				},
			},
		},
		recorder: record.NewFakeRecorder(32),
	}

	controlPlane := &internal.ControlPlane{
		KCP:      kcp,
		Cluster:  cluster,
		Machines: machines,
	}
	controlPlane.InjectTestManagementCluster(r.managementCluster)

	g.Expect(r.updateV1Beta1Status(ctx, controlPlane)).To(Succeed())
	g.Expect(kcp.Status.Deprecated.V1Beta1.ReadyReplicas).To(BeEquivalentTo(1))
	g.Expect(kcp.Status.Deprecated.V1Beta1.UnavailableReplicas).To(BeEquivalentTo(4))
	g.Expect(kcp.Status.Deprecated.V1Beta1.FailureMessage).To(BeNil())
	g.Expect(kcp.Status.Deprecated.V1Beta1.FailureReason).To(BeEquivalentTo(""))
}

func TestKubeadmControlPlaneReconciler_machinesCreatedIsIsTrueEvenWhenTheNodesAreNotReady(t *testing.T) {
	g := NewWithT(t)

	cluster := &clusterv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "foo",
			Namespace: metav1.NamespaceDefault,
		},
	}

	kcp := &controlplanev1.KubeadmControlPlane{
		TypeMeta: metav1.TypeMeta{
			Kind:       "KubeadmControlPlane",
			APIVersion: controlplanev1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      "foo",
		},
		Spec: controlplanev1.KubeadmControlPlaneSpec{
			Version:  "v1.16.6",
			Replicas: ptr.To[int32](3),
			MachineTemplate: controlplanev1.KubeadmControlPlaneMachineTemplate{
				Spec: controlplanev1.KubeadmControlPlaneMachineTemplateSpec{
					InfrastructureRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: "test",
						Kind:     "UnknownInfraMachine",
						Name:     "foo",
					},
				},
			},
		},
	}
	webhook := &controlplanev1webhooks.KubeadmControlPlane{}
	g.Expect(webhook.Default(ctx, kcp)).To(Succeed())
	_, err := webhook.ValidateCreate(ctx, kcp)
	g.Expect(err).ToNot(HaveOccurred())
	machines := map[string]*clusterv1.Machine{}
	objs := []client.Object{cluster.DeepCopy(), kcp.DeepCopy()}
	// Create the desired number of machines
	for i := range 3 {
		name := fmt.Sprintf("test-%d", i)
		m, n := createMachineNodePair(name, cluster, kcp, false)
		machines[m.Name] = m
		objs = append(objs, n, m)
	}

	fakeClient := newFakeClient(objs...)

	// Set all the machines to `not ready`
	r := &KubeadmControlPlaneReconciler{
		Client: fakeClient,
		managementCluster: &fakeManagementCluster{
			Machines: machines,
			Workload: &fakeWorkloadCluster{
				Status: internal.ClusterStatus{
					Nodes:            0,
					ReadyNodes:       0,
					HasKubeadmConfig: true,
				},
			},
		},
		recorder: record.NewFakeRecorder(32),
	}

	controlPlane := &internal.ControlPlane{
		KCP:      kcp,
		Cluster:  cluster,
		Machines: machines,
	}
	controlPlane.InjectTestManagementCluster(r.managementCluster)

	g.Expect(r.updateV1Beta1Status(ctx, controlPlane)).To(Succeed())
	g.Expect(kcp.Status.Deprecated.V1Beta1.ReadyReplicas).To(BeEquivalentTo(0))
	g.Expect(kcp.Status.Deprecated.V1Beta1.UnavailableReplicas).To(BeEquivalentTo(3))
	g.Expect(v1beta1conditions.IsTrue(kcp, controlplanev1.MachinesCreatedV1Beta1Condition)).To(BeTrue())
}

func kubeadmConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubeadm-config",
			Namespace: metav1.NamespaceSystem,
		},
	}
}
