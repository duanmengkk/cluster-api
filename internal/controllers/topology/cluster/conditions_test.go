/*
Copyright 2021 The Kubernetes Authors.

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

package cluster

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilfeature "k8s.io/component-base/featuregate/testing"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	runtimehooksv1 "sigs.k8s.io/cluster-api/api/runtime/hooks/v1alpha1"
	"sigs.k8s.io/cluster-api/exp/topology/scope"
	"sigs.k8s.io/cluster-api/feature"
	"sigs.k8s.io/cluster-api/util/conditions"
	v1beta1conditions "sigs.k8s.io/cluster-api/util/conditions/deprecated/v1beta1"
	"sigs.k8s.io/cluster-api/util/test/builder"
)

func TestReconcileTopologyReconciledCondition(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(clusterv1.AddToScheme(scheme)).To(Succeed())

	deletionTime := metav1.Unix(0, 0)
	tests := []struct {
		name                        string
		reconcileErr                error
		s                           *scope.Scope
		cluster                     *clusterv1.Cluster
		machines                    []*clusterv1.Machine
		wantConditionStatus         corev1.ConditionStatus
		wantConditionReason         string
		wantConditionMessage        string
		wantV1Beta2ConditionStatus  metav1.ConditionStatus
		wantV1Beta2ConditionReason  string
		wantV1Beta2ConditionMessage string
		wantErr                     bool
	}{
		{
			name:                        "should set the condition to false if there is a reconcile error",
			reconcileErr:                errors.New("reconcile error"),
			cluster:                     &clusterv1.Cluster{},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconcileFailedV1Beta1Reason,
			wantConditionMessage:        "reconcile error",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledFailedReason,
			wantV1Beta2ConditionMessage: "reconcile error",
			wantErr:                     false,
		},
		{
			name:    "should set the condition to false if the ClusterClass is out of date",
			cluster: &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					ClusterClass: &clusterv1.ClusterClass{
						ObjectMeta: metav1.ObjectMeta{
							Name:       "class1",
							Generation: 10,
						},
						Status: clusterv1.ClusterClassStatus{
							ObservedGeneration: 999,
						},
					},
				},
			},
			wantConditionStatus: corev1.ConditionFalse,
			wantConditionReason: clusterv1.TopologyReconciledClusterClassNotReconciledV1Beta1Reason,
			wantConditionMessage: "ClusterClass not reconciled. If this condition persists please check ClusterClass status. A ClusterClass is reconciled if" +
				".status.observedGeneration == .metadata.generation is true. If this is not the case either ClusterClass reconciliation failed or the ClusterClass is paused",
			wantV1Beta2ConditionStatus: metav1.ConditionFalse,
			wantV1Beta2ConditionReason: clusterv1.ClusterTopologyReconciledClusterClassNotReconciledReason,
			wantV1Beta2ConditionMessage: "ClusterClass not reconciled. If this condition persists please check ClusterClass status. A ClusterClass is reconciled if" +
				".status.observedGeneration == .metadata.generation is true. If this is not the case either ClusterClass reconciliation failed or the ClusterClass is paused",
			wantErr: false,
		},
		{
			name:         "should set the condition to false if the there is a blocking annotation hook",
			reconcileErr: nil,
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						clusterv1.BeforeClusterUpgradeHookAnnotationPrefix + "/test": "true",
					},
				},
			},
			s: &scope.Scope{
				HookResponseTracker: func() *scope.HookResponseTracker {
					hrt := scope.NewHookResponseTracker()
					hrt.Add(runtimehooksv1.BeforeClusterUpgrade, &runtimehooksv1.BeforeClusterUpgradeResponse{
						CommonRetryResponse: runtimehooksv1.CommonRetryResponse{
							CommonResponse: runtimehooksv1.CommonResponse{
								Message: fmt.Sprintf("annotation [%s] is set", clusterv1.BeforeClusterUpgradeHookAnnotationPrefix+"/test"),
							},
							RetryAfterSeconds: int32(20 * 60),
						},
					})
					return hrt
				}(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledHookBlockingV1Beta1Reason,
			wantConditionMessage:        "hook \"BeforeClusterUpgrade\" is blocking: annotation [" + clusterv1.BeforeClusterUpgradeHookAnnotationPrefix + "/test] is set",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledHookBlockingReason,
			wantV1Beta2ConditionMessage: "hook \"BeforeClusterUpgrade\" is blocking: annotation [" + clusterv1.BeforeClusterUpgradeHookAnnotationPrefix + "/test] is set",
		},
		{
			name:         "should set the condition to false if the there are multiple blocking annotation hooks",
			reconcileErr: nil,
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						clusterv1.BeforeClusterUpgradeHookAnnotationPrefix + "/test":  "true",
						clusterv1.BeforeClusterUpgradeHookAnnotationPrefix + "/test2": "true",
					},
				},
			},
			s: &scope.Scope{
				HookResponseTracker: func() *scope.HookResponseTracker {
					hrt := scope.NewHookResponseTracker()
					hrt.Add(runtimehooksv1.BeforeClusterUpgrade, &runtimehooksv1.BeforeClusterUpgradeResponse{
						CommonRetryResponse: runtimehooksv1.CommonRetryResponse{
							CommonResponse: runtimehooksv1.CommonResponse{
								Message: "annotations [" + clusterv1.BeforeClusterUpgradeHookAnnotationPrefix + "/test, " + clusterv1.BeforeClusterUpgradeHookAnnotationPrefix + "/test2] are set",
							},
							RetryAfterSeconds: 20 * 60,
						},
					})
					return hrt
				}(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledHookBlockingV1Beta1Reason,
			wantConditionMessage:        "hook \"BeforeClusterUpgrade\" is blocking: annotations [" + clusterv1.BeforeClusterUpgradeHookAnnotationPrefix + "/test, " + clusterv1.BeforeClusterUpgradeHookAnnotationPrefix + "/test2] are set",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledHookBlockingReason,
			wantV1Beta2ConditionMessage: "hook \"BeforeClusterUpgrade\" is blocking: annotations [" + clusterv1.BeforeClusterUpgradeHookAnnotationPrefix + "/test, " + clusterv1.BeforeClusterUpgradeHookAnnotationPrefix + "/test2] are set",
		},
		{
			name:         "should set the condition to false if the there is a blocking hook",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				HookResponseTracker: func() *scope.HookResponseTracker {
					hrt := scope.NewHookResponseTracker()
					hrt.Add(runtimehooksv1.BeforeClusterUpgrade, &runtimehooksv1.BeforeClusterUpgradeResponse{
						CommonRetryResponse: runtimehooksv1.CommonRetryResponse{
							CommonResponse: runtimehooksv1.CommonResponse{
								Message: "msg",
							},
							RetryAfterSeconds: 10,
						},
					})
					return hrt
				}(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledHookBlockingV1Beta1Reason,
			wantConditionMessage:        "hook \"BeforeClusterUpgrade\" is blocking: msg",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledHookBlockingReason,
			wantV1Beta2ConditionMessage: "hook \"BeforeClusterUpgrade\" is blocking: msg",
		},
		{
			name:         "should set the condition to false if new version is not picked up because control plane is provisioning",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.21.2").
							Build(),
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = true
					ut.ControlPlane.IsProvisioning = true
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledControlPlaneUpgradePendingV1Beta1Reason,
			wantConditionMessage:        "Control plane rollout and upgrade to version v1.22.0 on hold. Control plane is completing initial provisioning",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledControlPlaneUpgradePendingReason,
			wantV1Beta2ConditionMessage: "Control plane rollout and upgrade to version v1.22.0 on hold. Control plane is completing initial provisioning",
		},
		{
			name:         "should set the condition to false if new version is not picked up because control plane is upgrading",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.21.2").
							WithReplicas(3).
							Build(),
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = true
					ut.ControlPlane.IsUpgrading = true
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledControlPlaneUpgradePendingV1Beta1Reason,
			wantConditionMessage:        "Control plane rollout and upgrade to version v1.22.0 on hold. Control plane is upgrading to version v1.21.2",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledControlPlaneUpgradePendingReason,
			wantV1Beta2ConditionMessage: "Control plane rollout and upgrade to version v1.22.0 on hold. Control plane is upgrading to version v1.21.2",
		},
		{
			name:         "should set the condition to false if new version is not picked up because at least one of the machine deployment is upgrading",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.21.2").
							WithReplicas(3).
							Build(),
					},
					MachineDeployments: scope.MachineDeploymentsStateMap{
						"md0": &scope.MachineDeploymentState{
							Object: builder.MachineDeployment("ns1", "md0-abc123").
								WithReplicas(2).
								WithStatus(clusterv1.MachineDeploymentStatus{
									Replicas:          ptr.To[int32](1),
									ReadyReplicas:     ptr.To[int32](1),
									UpToDateReplicas:  ptr.To[int32](1),
									AvailableReplicas: ptr.To[int32](1),
								}).
								Build(),
						},
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = true
					ut.MachineDeployments.MarkUpgrading("md0-abc123")
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledControlPlaneUpgradePendingV1Beta1Reason,
			wantConditionMessage:        "Control plane rollout and upgrade to version v1.22.0 on hold. MachineDeployment(s) md0-abc123 are upgrading",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledControlPlaneUpgradePendingReason,
			wantV1Beta2ConditionMessage: "Control plane rollout and upgrade to version v1.22.0 on hold. MachineDeployment(s) md0-abc123 are upgrading",
		},
		{
			name:         "should set the condition to false if new version is not picked up because at least one of the machine pool is upgrading",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.21.2").
							WithReplicas(3).
							Build(),
					},
					MachinePools: scope.MachinePoolsStateMap{
						"mp0": &scope.MachinePoolState{
							Object: builder.MachinePool("ns1", "mp0-abc123").
								WithReplicas(2).
								WithStatus(clusterv1.MachinePoolStatus{
									Replicas: ptr.To(int32(1)),
									Deprecated: &clusterv1.MachinePoolDeprecatedStatus{
										V1Beta1: &clusterv1.MachinePoolV1Beta1DeprecatedStatus{
											ReadyReplicas:       int32(1),
											AvailableReplicas:   int32(1),
											UnavailableReplicas: int32(0),
										},
									},
								}).
								Build(),
						},
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = true
					ut.MachinePools.MarkUpgrading("mp0-abc123")
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledControlPlaneUpgradePendingV1Beta1Reason,
			wantConditionMessage:        "Control plane rollout and upgrade to version v1.22.0 on hold. MachinePool(s) mp0-abc123 are upgrading",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledControlPlaneUpgradePendingReason,
			wantV1Beta2ConditionMessage: "Control plane rollout and upgrade to version v1.22.0 on hold. MachinePool(s) mp0-abc123 are upgrading",
		},
		{
			name:         "should set the condition to false if control plane picked the new version but machine deployments did not because control plane is upgrading",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.22.0").
							WithReplicas(3).
							Build(),
					},
					MachineDeployments: scope.MachineDeploymentsStateMap{
						"md0": &scope.MachineDeploymentState{
							Object: builder.MachineDeployment("ns1", "md0-abc123").
								WithReplicas(2).
								WithStatus(clusterv1.MachineDeploymentStatus{
									Replicas:          ptr.To[int32](2),
									ReadyReplicas:     ptr.To[int32](2),
									UpToDateReplicas:  ptr.To[int32](2),
									AvailableReplicas: ptr.To[int32](2),
								}).
								Build(),
						},
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = false
					ut.ControlPlane.IsUpgrading = true
					ut.MachineDeployments.MarkPendingUpgrade("md0-abc123")
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledMachineDeploymentsUpgradePendingV1Beta1Reason,
			wantConditionMessage:        "MachineDeployment(s) md0-abc123 rollout and upgrade to version v1.22.0 on hold. Control plane is upgrading to version v1.22.0",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledMachineDeploymentsUpgradePendingReason,
			wantV1Beta2ConditionMessage: "MachineDeployment(s) md0-abc123 rollout and upgrade to version v1.22.0 on hold. Control plane is upgrading to version v1.22.0",
		},
		{
			name:         "should set the condition to false if control plane picked the new version but machine pools did not because control plane is upgrading",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.22.0").
							WithReplicas(3).
							Build(),
					},
					MachinePools: scope.MachinePoolsStateMap{
						"mp0": &scope.MachinePoolState{
							Object: builder.MachinePool("ns1", "mp0-abc123").
								WithReplicas(2).
								WithStatus(clusterv1.MachinePoolStatus{
									Replicas: ptr.To(int32(2)),
									Deprecated: &clusterv1.MachinePoolDeprecatedStatus{
										V1Beta1: &clusterv1.MachinePoolV1Beta1DeprecatedStatus{
											ReadyReplicas:       int32(2),
											AvailableReplicas:   int32(2),
											UnavailableReplicas: int32(0),
										},
									},
								}).
								Build(),
						},
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = false
					ut.ControlPlane.IsUpgrading = true
					ut.MachinePools.MarkPendingUpgrade("mp0-abc123")
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledMachinePoolsUpgradePendingV1Beta1Reason,
			wantConditionMessage:        "MachinePool(s) mp0-abc123 rollout and upgrade to version v1.22.0 on hold. Control plane is upgrading to version v1.22.0",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledMachinePoolsUpgradePendingReason,
			wantV1Beta2ConditionMessage: "MachinePool(s) mp0-abc123 rollout and upgrade to version v1.22.0 on hold. Control plane is upgrading to version v1.22.0",
		},
		{
			name:         "should set the condition to true if control plane picked the new version and is upgrading but there are no machine deployments or machine pools",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.22.0").
							WithReplicas(3).
							Build(),
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = false
					ut.ControlPlane.IsUpgrading = true
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			wantConditionStatus:         corev1.ConditionTrue,
			wantV1Beta2ConditionStatus:  metav1.ConditionTrue,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconcileSucceededReason,
			wantV1Beta2ConditionMessage: "",
		},
		{
			name:         "should set the condition to true if control plane picked the new version and is scaling but there are no machine deployments or machine pools",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.22.0").
							WithReplicas(3).
							Build(),
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = false
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			wantConditionStatus:         corev1.ConditionTrue,
			wantV1Beta2ConditionStatus:  metav1.ConditionTrue,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconcileSucceededReason,
			wantV1Beta2ConditionMessage: "",
		},
		{
			name:         "should set the condition to false is some machine deployments have not picked the new version because other machine deployments are upgrading",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.22.0").
							WithReplicas(3).
							Build(),
					},
					MachineDeployments: scope.MachineDeploymentsStateMap{
						"md0": &scope.MachineDeploymentState{
							Object: builder.MachineDeployment("ns1", "md0-abc123").
								WithReplicas(2).
								WithVersion("v1.22.0").
								WithSelector(metav1.LabelSelector{
									MatchLabels: map[string]string{
										clusterv1.ClusterTopologyMachineDeploymentNameLabel: "md0",
									},
								}).
								WithStatus(clusterv1.MachineDeploymentStatus{
									// MD is not ready because we don't have 2 updated, ready and available replicas.
									Replicas:          ptr.To[int32](2),
									ReadyReplicas:     ptr.To[int32](1),
									UpToDateReplicas:  ptr.To[int32](1),
									AvailableReplicas: ptr.To[int32](1),
								}).
								Build(),
						},
						"md1": &scope.MachineDeploymentState{
							Object: builder.MachineDeployment("ns1", "md1-abc123").
								WithReplicas(2).
								WithVersion("v1.21.2").
								WithSelector(metav1.LabelSelector{
									MatchLabels: map[string]string{
										clusterv1.ClusterTopologyMachineDeploymentNameLabel: "md1",
									},
								}).
								WithStatus(clusterv1.MachineDeploymentStatus{
									Replicas:          ptr.To[int32](2),
									ReadyReplicas:     ptr.To[int32](2),
									UpToDateReplicas:  ptr.To[int32](2),
									AvailableReplicas: ptr.To[int32](2),
								}).
								Build(),
						},
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = false
					ut.MachineDeployments.MarkUpgrading("md0-abc123")
					ut.MachineDeployments.MarkPendingUpgrade("md1-abc123")
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			machines: []*clusterv1.Machine{
				builder.Machine("ns1", "md0-machine0").
					WithLabels(map[string]string{clusterv1.ClusterTopologyMachineDeploymentNameLabel: "md0"}).
					WithVersion("v1.21.2"). // Machine's version does not match MachineDeployment's version
					Build(),
				builder.Machine("ns1", "md1-machine0").
					WithLabels(map[string]string{clusterv1.ClusterTopologyMachineDeploymentNameLabel: "md1"}).
					WithVersion("v1.21.2").
					Build(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledMachineDeploymentsUpgradePendingV1Beta1Reason,
			wantConditionMessage:        "MachineDeployment(s) md1-abc123 rollout and upgrade to version v1.22.0 on hold. MachineDeployment(s) md0-abc123 are upgrading",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledMachineDeploymentsUpgradePendingReason,
			wantV1Beta2ConditionMessage: "MachineDeployment(s) md1-abc123 rollout and upgrade to version v1.22.0 on hold. MachineDeployment(s) md0-abc123 are upgrading",
		},
		{
			name:         "should set the condition to false is some machine pools have not picked the new version because other machine pools are upgrading",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.22.0").
							WithReplicas(3).
							Build(),
					},
					MachinePools: scope.MachinePoolsStateMap{
						"mp0": &scope.MachinePoolState{
							Object: builder.MachinePool("ns1", "mp0-abc123").
								WithReplicas(2).
								WithVersion("v1.22.0").
								WithStatus(clusterv1.MachinePoolStatus{
									// mp is not ready because we don't have 2 updated, ready and available replicas.
									Replicas: ptr.To(int32(2)),
									Deprecated: &clusterv1.MachinePoolDeprecatedStatus{
										V1Beta1: &clusterv1.MachinePoolV1Beta1DeprecatedStatus{
											ReadyReplicas:       int32(1),
											AvailableReplicas:   int32(1),
											UnavailableReplicas: int32(0),
										},
									},
								}).
								Build(),
						},
						"mp1": &scope.MachinePoolState{
							Object: builder.MachinePool("ns1", "mp1-abc123").
								WithReplicas(2).
								WithVersion("v1.21.2").
								WithStatus(clusterv1.MachinePoolStatus{
									Replicas: ptr.To(int32(2)),
									Deprecated: &clusterv1.MachinePoolDeprecatedStatus{
										V1Beta1: &clusterv1.MachinePoolV1Beta1DeprecatedStatus{
											ReadyReplicas:       int32(2),
											AvailableReplicas:   int32(2),
											UnavailableReplicas: int32(0),
										},
									},
								}).
								Build(),
						},
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = false
					ut.MachinePools.MarkUpgrading("mp0-abc123")
					ut.MachinePools.MarkPendingUpgrade("mp1-abc123")
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			machines: []*clusterv1.Machine{
				builder.Machine("ns1", "mp0-machine0").
					WithLabels(map[string]string{clusterv1.ClusterTopologyMachinePoolNameLabel: "mp0"}).
					WithVersion("v1.21.2"). // Machine's version does not match MachinePool's version
					Build(),
				builder.Machine("ns1", "mp1-machine0").
					WithLabels(map[string]string{clusterv1.ClusterTopologyMachinePoolNameLabel: "mp1"}).
					WithVersion("v1.21.2").
					Build(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledMachinePoolsUpgradePendingV1Beta1Reason,
			wantConditionMessage:        "MachinePool(s) mp1-abc123 rollout and upgrade to version v1.22.0 on hold. MachinePool(s) mp0-abc123 are upgrading",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledMachinePoolsUpgradePendingReason,
			wantV1Beta2ConditionMessage: "MachinePool(s) mp1-abc123 rollout and upgrade to version v1.22.0 on hold. MachinePool(s) mp0-abc123 are upgrading",
		},
		{
			name:         "should set the condition to false if some machine deployments have not picked the new version because their upgrade has been deferred",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.22.0").
							WithReplicas(3).
							Build(),
					},
					MachineDeployments: scope.MachineDeploymentsStateMap{
						"md0": &scope.MachineDeploymentState{
							Object: builder.MachineDeployment("ns1", "md0-abc123").
								WithReplicas(2).
								WithVersion("v1.22.0").
								WithStatus(clusterv1.MachineDeploymentStatus{
									Replicas:          ptr.To[int32](2),
									ReadyReplicas:     ptr.To[int32](2),
									UpToDateReplicas:  ptr.To[int32](2),
									AvailableReplicas: ptr.To[int32](2),
								}).
								Build(),
						},
						"md1": &scope.MachineDeploymentState{
							Object: builder.MachineDeployment("ns1", "md1-abc123").
								WithReplicas(2).
								WithVersion("v1.21.2").
								WithStatus(clusterv1.MachineDeploymentStatus{
									Replicas:          ptr.To[int32](2),
									ReadyReplicas:     ptr.To[int32](2),
									UpToDateReplicas:  ptr.To[int32](2),
									AvailableReplicas: ptr.To[int32](2),
								}).
								Build(),
						},
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = false
					ut.MachineDeployments.MarkDeferredUpgrade("md1-abc123")
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledMachineDeploymentsUpgradeDeferredV1Beta1Reason,
			wantConditionMessage:        "MachineDeployment(s) md1-abc123 rollout and upgrade to version v1.22.0 deferred.",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledMachineDeploymentsUpgradeDeferredReason,
			wantV1Beta2ConditionMessage: "MachineDeployment(s) md1-abc123 rollout and upgrade to version v1.22.0 deferred.",
		},
		{
			name:         "should set the condition to false if some machine pools have not picked the new version because their upgrade has been deferred",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.22.0").
							WithReplicas(3).
							Build(),
					},
					MachinePools: scope.MachinePoolsStateMap{
						"mp0": &scope.MachinePoolState{
							Object: builder.MachinePool("ns1", "mp0-abc123").
								WithReplicas(2).
								WithVersion("v1.22.0").
								WithStatus(clusterv1.MachinePoolStatus{
									Replicas: ptr.To(int32(2)),
									Deprecated: &clusterv1.MachinePoolDeprecatedStatus{
										V1Beta1: &clusterv1.MachinePoolV1Beta1DeprecatedStatus{
											ReadyReplicas:       int32(2),
											AvailableReplicas:   int32(2),
											UnavailableReplicas: int32(0),
										},
									},
								}).
								Build(),
						},
						"mp1": &scope.MachinePoolState{
							Object: builder.MachinePool("ns1", "mp1-abc123").
								WithReplicas(2).
								WithVersion("v1.21.2").
								WithStatus(clusterv1.MachinePoolStatus{
									Replicas: ptr.To(int32(2)),
									Deprecated: &clusterv1.MachinePoolDeprecatedStatus{
										V1Beta1: &clusterv1.MachinePoolV1Beta1DeprecatedStatus{
											ReadyReplicas:       int32(2),
											AvailableReplicas:   int32(2),
											UnavailableReplicas: int32(0),
										},
									},
								}).
								Build(),
						},
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = false
					ut.MachinePools.MarkDeferredUpgrade("mp1-abc123")
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.TopologyReconciledMachinePoolsUpgradeDeferredV1Beta1Reason,
			wantConditionMessage:        "MachinePool(s) mp1-abc123 rollout and upgrade to version v1.22.0 deferred.",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledMachinePoolsUpgradeDeferredReason,
			wantV1Beta2ConditionMessage: "MachinePool(s) mp1-abc123 rollout and upgrade to version v1.22.0 deferred.",
		},
		{
			name:         "should set the condition to true if there are no reconcile errors and control plane and all machine deployments and machine pools picked up the new version",
			reconcileErr: nil,
			cluster:      &clusterv1.Cluster{},
			s: &scope.Scope{
				Blueprint: &scope.ClusterBlueprint{
					Topology: clusterv1.Topology{
						Version: "v1.22.0",
					},
				},
				Current: &scope.ClusterState{
					Cluster: &clusterv1.Cluster{},
					ControlPlane: &scope.ControlPlaneState{
						Object: builder.ControlPlane("ns1", "controlplane1").
							WithVersion("v1.22.0").
							WithReplicas(3).
							Build(),
					},
					MachineDeployments: scope.MachineDeploymentsStateMap{
						"md0": &scope.MachineDeploymentState{
							Object: builder.MachineDeployment("ns1", "md0-abc123").
								WithReplicas(2).
								WithVersion("v1.22.0").
								WithStatus(clusterv1.MachineDeploymentStatus{
									Replicas:          ptr.To[int32](1),
									ReadyReplicas:     ptr.To[int32](1),
									UpToDateReplicas:  ptr.To[int32](1),
									AvailableReplicas: ptr.To[int32](1),
								}).
								Build(),
						},
						"md1": &scope.MachineDeploymentState{
							Object: builder.MachineDeployment("ns1", "md1-abc123").
								WithReplicas(2).
								WithVersion("v1.22.0").
								WithStatus(clusterv1.MachineDeploymentStatus{
									Replicas:          ptr.To[int32](2),
									ReadyReplicas:     ptr.To[int32](2),
									UpToDateReplicas:  ptr.To[int32](2),
									AvailableReplicas: ptr.To[int32](2),
								}).
								Build(),
						},
					},
					MachinePools: scope.MachinePoolsStateMap{
						"mp0": &scope.MachinePoolState{
							Object: builder.MachinePool("ns1", "mp0-abc123").
								WithReplicas(2).
								WithVersion("v1.22.0").
								WithStatus(clusterv1.MachinePoolStatus{
									Replicas: ptr.To(int32(1)),
									Deprecated: &clusterv1.MachinePoolDeprecatedStatus{
										V1Beta1: &clusterv1.MachinePoolV1Beta1DeprecatedStatus{
											ReadyReplicas:       int32(1),
											AvailableReplicas:   int32(1),
											UnavailableReplicas: int32(0),
										},
									},
								}).
								Build(),
						},
						"mp1": &scope.MachinePoolState{
							Object: builder.MachinePool("ns1", "mp1-abc123").
								WithReplicas(2).
								WithVersion("v1.22.0").
								WithStatus(clusterv1.MachinePoolStatus{
									Replicas: ptr.To(int32(2)),
									Deprecated: &clusterv1.MachinePoolDeprecatedStatus{
										V1Beta1: &clusterv1.MachinePoolV1Beta1DeprecatedStatus{
											ReadyReplicas:       int32(2),
											AvailableReplicas:   int32(2),
											UnavailableReplicas: int32(0),
										},
									},
								}).
								Build(),
						},
					},
				},
				UpgradeTracker: func() *scope.UpgradeTracker {
					ut := scope.NewUpgradeTracker()
					ut.ControlPlane.IsPendingUpgrade = false
					return ut
				}(),
				HookResponseTracker: scope.NewHookResponseTracker(),
			},
			wantConditionStatus:         corev1.ConditionTrue,
			wantV1Beta2ConditionStatus:  metav1.ConditionTrue,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconcileSucceededReason,
			wantV1Beta2ConditionMessage: "",
		},
		{
			name: "should set the TopologyReconciledCondition to False if the cluster has been deleted",
			cluster: &clusterv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{
					DeletionTimestamp: &deletionTime,
				},
			},
			wantConditionStatus:         corev1.ConditionFalse,
			wantConditionReason:         clusterv1.DeletedV1Beta1Reason,
			wantConditionMessage:        "",
			wantV1Beta2ConditionStatus:  metav1.ConditionFalse,
			wantV1Beta2ConditionReason:  clusterv1.ClusterTopologyReconciledDeletingReason,
			wantV1Beta2ConditionMessage: "Cluster is deleting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			utilfeature.SetFeatureGateDuringTest(t, feature.Gates, feature.RuntimeSDK, true)

			objs := []client.Object{}
			if tt.s != nil && tt.s.Current != nil {
				for _, md := range tt.s.Current.MachineDeployments {
					objs = append(objs, md.Object)
				}
				for _, mp := range tt.s.Current.MachinePools {
					objs = append(objs, mp.Object)
				}
			}
			for _, m := range tt.machines {
				objs = append(objs, m)
			}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

			r := &Reconciler{Client: fakeClient}
			err := r.reconcileTopologyReconciledCondition(tt.s, tt.cluster, tt.reconcileErr)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())

				actualV1Beta1Condition := v1beta1conditions.Get(tt.cluster, clusterv1.TopologyReconciledV1Beta1Condition)
				g.Expect(actualV1Beta1Condition).ToNot(BeNil())
				g.Expect(actualV1Beta1Condition.Status).To(BeEquivalentTo(tt.wantConditionStatus))
				g.Expect(actualV1Beta1Condition.Reason).To(BeEquivalentTo(tt.wantConditionReason))
				g.Expect(actualV1Beta1Condition.Message).To(BeEquivalentTo(tt.wantConditionMessage))

				actualCondition := conditions.Get(tt.cluster, clusterv1.ClusterTopologyReconciledCondition)
				g.Expect(actualCondition).ToNot(BeNil())
				g.Expect(actualCondition.Status).To(BeEquivalentTo(tt.wantV1Beta2ConditionStatus))
				g.Expect(actualCondition.Reason).To(BeEquivalentTo(tt.wantV1Beta2ConditionReason))
				g.Expect(actualCondition.Message).To(BeEquivalentTo(tt.wantV1Beta2ConditionMessage))
			}
		})
	}
}

func TestComputeNameList(t *testing.T) {
	tests := []struct {
		name     string
		mdList   []string
		expected string
	}{
		{
			name:     "mdList with 4 names",
			mdList:   []string{"md-1", "md-2", "md-3", "md-4"},
			expected: "md-1, md-2, md-3, md-4",
		},
		{
			name:     "mdList with 5 names",
			mdList:   []string{"md-1", "md-2", "md-3", "md-4", "md-5"},
			expected: "md-1, md-2, md-3, md-4, md-5",
		},
		{
			name:     "mdList with 6 names is shortened",
			mdList:   []string{"md-1", "md-2", "md-3", "md-4", "md-5", "md-6"},
			expected: "md-1, md-2, md-3, md-4, md-5, ...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(computeNameList(tt.mdList)).To(Equal(tt.expected))
		})
	}
}
