/*
Copyright 2022.

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
	"context"
	"fmt"
	v1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
	"time"

	bigdatav1alpha1 "github.com/kubernetesbigdataeg/zookeeper-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Definitions to manage status conditions
const (
	zookeeperFinalizer = "bigdata.kubernetesbigdataeg.org/finalizer"
	// typeAvailableZookeeper represents the status of the Deployment reconciliation
	typeAvailableZookeeper = "Available"
	// typeDegradedZookeeper represents the status used when the custom resource
	// is deleted and the finalizer operations are must to occur.
	typeDegradedZookeeper = "Degraded"
)

// ZookeeperReconciler reconciles a Zookeeper object
type ZookeeperReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// Kubebuilder makes use of a tool called controller-gen for generating utility code
// and Kubernetes YAML. This code and config generation is controlled by the presence
// of special “marker comments” in Go code. Markers are single-line comments that start with
// a plus, followed by a marker name, optionally followed by some marker specific configuration
// The following markers are used to generate the rules permissions (RBAC) on
// config/rbac using controller-gen when the command <make manifests> is executed.
// To know more about markers see: https://book.kubebuilder.io/reference/markers.html

// DO NOT REMOVE
//+kubebuilder:rbac:groups=bigdata.kubernetesbigdataeg.org,resources=zookeepers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=bigdata.kubernetesbigdataeg.org,resources=zookeepers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=bigdata.kubernetesbigdataeg.org,resources=zookeepers/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop, which aims to
// move the current state of the cluster closer to the desired state.
// It is essential for the controller's reconciliation loop to be idempotent.
// By following the Operator pattern you will create Controllers which provide a reconcile function
// responsible for synchronizing resources until the desired state is reached on the cluster.
// Breaking this recommendation goes against the design principles of controller-runtime.
// and may lead to unforeseen consequences such as resources becoming stuck and requiring manual intervention.
// For further info:
//   - About Operator Pattern: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/
//   - About Controllers: https://kubernetes.io/docs/concepts/architecture/controller/
//   - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *ZookeeperReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	//
	// 1. Control-loop: checking if Zookeeper CR exists
	//
	// The purpose is check if the Custom Resource for the Kind Zookeeper
	// is applied on the cluster if not we return nil to stop the reconciliation
	zookeeper := &bigdatav1alpha1.Zookeeper{}
	err := r.Get(ctx, req.NamespacedName, zookeeper)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the custom resource is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("zookeeper resource (CR) not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get zookeeper")
		return ctrl.Result{}, err
	}

	//
	// 2. Control-loop: Status to Unknown
	//
	// Let's just set the status as Unknown when no status are available
	if zookeeper.Status.Conditions == nil || len(zookeeper.Status.Conditions) == 0 {
		meta.SetStatusCondition(&zookeeper.Status.Conditions, metav1.Condition{Type: typeAvailableZookeeper,
			Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})

		if err = r.Status().Update(ctx, zookeeper); err != nil {
			log.Error(err, "Failed to update Zookeeper status")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the zookeeper Custom Resource after update the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, zookeeper); err != nil {
			log.Error(err, "Failed to re-fetch zookeeper")
			return ctrl.Result{}, err
		}
	}

	//
	// 3. Control-loop: Let's add a finalizer
	//
	// Then, we can define some operations which should occurs before the custom resource to be deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(zookeeper, zookeeperFinalizer) {
		log.Info("Adding Finalizer for Zookeeper")
		if ok := controllerutil.AddFinalizer(zookeeper, zookeeperFinalizer); !ok {
			log.Error(err, "Failed to add finalizer into the custom resource")
			return ctrl.Result{Requeue: true}, nil
		}

		if err = r.Update(ctx, zookeeper); err != nil {
			log.Error(err, "Failed to update custom resource to add finalizer")
			return ctrl.Result{}, err
		}
	}

	//
	// 4. Control-loop: Instance marked for deletion
	//
	// Check if the Zookeeper instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isZookeeperMarkedToBeDeleted := zookeeper.GetDeletionTimestamp() != nil
	if isZookeeperMarkedToBeDeleted {
		if controllerutil.ContainsFinalizer(zookeeper, zookeeperFinalizer) {
			log.Info("Performing Finalizer Operations for Zookeeper before delete CR")

			// Let's add here an status "Downgrade" to define that this resource begin its process to be terminated.
			meta.SetStatusCondition(&zookeeper.Status.Conditions, metav1.Condition{Type: typeDegradedZookeeper,
				Status: metav1.ConditionUnknown, Reason: "Finalizing",
				Message: fmt.Sprintf("Performing finalizer operations for the custom resource: %s ", zookeeper.Name)})

			if err := r.Status().Update(ctx, zookeeper); err != nil {
				log.Error(err, "Failed to update Zookeeper status")
				return ctrl.Result{}, err
			}

			// Perform all operations required before remove the finalizer and allow
			// the Kubernetes API to remove the custom custom resource.
			r.doFinalizerOperationsForZookeeper(zookeeper)

			// TODO(user): If you add operations to the doFinalizerOperationsForZookeeper method
			// then you need to ensure that all worked fine before deleting and updating the Downgrade status
			// otherwise, you should requeue here.

			// Re-fetch the zookeeper Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, zookeeper); err != nil {
				log.Error(err, "Failed to re-fetch zookeeper")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&zookeeper.Status.Conditions, metav1.Condition{Type: typeDegradedZookeeper,
				Status: metav1.ConditionTrue, Reason: "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for custom resource %s name were successfully accomplished", zookeeper.Name)})

			if err := r.Status().Update(ctx, zookeeper); err != nil {
				log.Error(err, "Failed to update Zookeeper status")
				return ctrl.Result{}, err
			}

			log.Info("Removing Finalizer for Zookeeper after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(zookeeper, zookeeperFinalizer); !ok {
				log.Error(err, "Failed to remove finalizer for Zookeeper")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.Update(ctx, zookeeper); err != nil {
				log.Error(err, "Failed to remove finalizer for Zookeeper")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	//
	// 5. Control-loop: Let's deploy/ensure our managed resources for Zookeeper
	// - ConfigMap,
	// - PodDisruptionBudget,
	// - Service Headless,
	// - Service ClusterIP,
	// - StateFulSet

	// ConfigMap: Check if the cm already exists, if not create a new one
	configMapFound := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: "zk-config", Namespace: zookeeper.Namespace}, configMapFound)
	if err != nil && apierrors.IsNotFound(err) {
		// Define the default ConfigMap
		cm, err := r.defaultConfigMapForZookeeper(zookeeper)
		if err != nil {
			log.Error(err, "Failed to define new ConfigMap resource for Zookeeper")

			// The following implementation will update the status
			meta.SetStatusCondition(&zookeeper.Status.Conditions, metav1.Condition{Type: typeAvailableZookeeper,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create ConfigMap for the custom resource (%s): (%s)",
					zookeeper.Name, err)})

			if err := r.Status().Update(ctx, zookeeper); err != nil {
				log.Error(err, "Failed to update Zookeeper status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		log.Info("Creating a new ConfigMap",
			"ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)

		if err = r.Create(ctx, cm); err != nil {
			log.Error(err, "Failed to create new ConfigMap",
				"ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
			return ctrl.Result{}, err
		}

		// ConfigMap created successfully at this point.
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		//return ctrl.Result{RequeueAfter: time.Minute}, nil

	} else if err != nil {
		log.Error(err, "Failed to get ConfigMap")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	// PodDisruptionBudget: Check if the pbd already exists, if not create a new one
	pdbFound := &v1.PodDisruptionBudget{}
	err = r.Get(ctx, types.NamespacedName{Name: "zk-pdb", Namespace: zookeeper.Namespace}, pdbFound)
	if err != nil && apierrors.IsNotFound(err) {
		// Define the pdb resource
		pdb, err := r.pdbForZookeeper(zookeeper)
		if err != nil {
			log.Error(err, "Failed to define new PodDisruptionBudget resource for Zookeeper")

			// The following implementation will update the status
			meta.SetStatusCondition(&zookeeper.Status.Conditions, metav1.Condition{Type: typeAvailableZookeeper,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create PodDisruptionBudget for the custom resource (%s): (%s)",
					zookeeper.Name, err)})

			if err := r.Status().Update(ctx, zookeeper); err != nil {
				log.Error(err, "Failed to update Zookeeper status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		log.Info("Creating a new PodDisruptionBudget",
			"PodDisruptionBudget.Namespace", pdb.Namespace, "PodDisruptionBudget.Name", pdb.Name)

		if err = r.Create(ctx, pdb); err != nil {
			log.Error(err, "Failed to create new PodDisruptionBudget",
				"PodDisruptionBudget.Namespace", pdb.Namespace, "PodDisruptionBudget.Name", pdb.Name)
			return ctrl.Result{}, err
		}

		// PodDisruptionBudget created successfully at this point.
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		//return ctrl.Result{RequeueAfter: time.Minute}, nil

	} else if err != nil {
		log.Error(err, "Failed to get PodDisruptionBudget")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	// Service Headless: Check if the headless svc already exists, if not create a new one
	serviceHeadlessFound := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: "zk-hs", Namespace: zookeeper.Namespace}, serviceHeadlessFound)
	if err != nil && apierrors.IsNotFound(err) {
		// Define the pdb resource
		hsvc, err := r.serviceHeadlessForZookeeper(zookeeper)
		if err != nil {
			log.Error(err, "Failed to define new Headless Service resource for Zookeeper")

			// The following implementation will update the status
			meta.SetStatusCondition(&zookeeper.Status.Conditions, metav1.Condition{Type: typeAvailableZookeeper,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create Headless Service for the custom resource (%s): (%s)",
					zookeeper.Name, err)})

			if err := r.Status().Update(ctx, zookeeper); err != nil {
				log.Error(err, "Failed to update Zookeeper status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		log.Info("Creating a new Headless Service",
			"Service.Namespace", hsvc.Namespace, "Service.Name", hsvc.Name)

		if err = r.Create(ctx, hsvc); err != nil {
			log.Error(err, "Failed to create new Headless Service",
				"Service.Namespace", hsvc.Namespace, "Service.Name", hsvc.Name)
			return ctrl.Result{}, err
		}

		// Service created successfully at this point.
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		//return ctrl.Result{RequeueAfter: time.Minute}, nil

	} else if err != nil {
		log.Error(err, "Failed to get Headless Service")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	// Service: Check if the headless svc already exists, if not create a new one
	serviceFound := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: "zk-cs", Namespace: zookeeper.Namespace}, serviceFound)
	if err != nil && apierrors.IsNotFound(err) {
		// Define the pdb resource
		svc, err := r.serviceForZookeeper(zookeeper)
		if err != nil {
			log.Error(err, "Failed to define new Service resource for Zookeeper")

			// The following implementation will update the status
			meta.SetStatusCondition(&zookeeper.Status.Conditions, metav1.Condition{Type: typeAvailableZookeeper,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create Service for the custom resource (%s): (%s)",
					zookeeper.Name, err)})

			if err := r.Status().Update(ctx, zookeeper); err != nil {
				log.Error(err, "Failed to update Zookeeper status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		log.Info("Creating a new Service",
			"Service.Namespace", svc.Namespace, "Service.Name", svc.Name)

		if err = r.Create(ctx, svc); err != nil {
			log.Error(err, "Failed to create new Service",
				"Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
			return ctrl.Result{}, err
		}

		// Service created successfully at this point.
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		//return ctrl.Result{RequeueAfter: time.Minute}, nil

	} else if err != nil {
		log.Error(err, "Failed to get Headless Service")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	// StateFulSet: Check if the sts already exists, if not create a new one
	found := &appsv1.StatefulSet{}
	err = r.Get(ctx, types.NamespacedName{Name: zookeeper.Name, Namespace: zookeeper.Namespace}, found)
	if err != nil && apierrors.IsNotFound(err) {
		// Define a new sts
		dep, err := r.stateFulSetForZookeeper(zookeeper)
		if err != nil {
			log.Error(err, "Failed to define new StateFulSet resource for Zookeeper")

			// The following implementation will update the status
			meta.SetStatusCondition(&zookeeper.Status.Conditions, metav1.Condition{Type: typeAvailableZookeeper,
				Status: metav1.ConditionFalse, Reason: "Reconciling",
				Message: fmt.Sprintf("Failed to create StatefulSet for the custom resource (%s): (%s)", zookeeper.Name, err)})

			if err := r.Status().Update(ctx, zookeeper); err != nil {
				log.Error(err, "Failed to update Zookeeper status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		log.Info("Creating a new StateFulSet",
			"StatefulSet.Namespace", dep.Namespace, "StatefulSet.Name", dep.Name)

		if err = r.Create(ctx, dep); err != nil {
			log.Error(err, "Failed to create new StatefulSet",
				"StatefulSet.Namespace", dep.Namespace, "StatefulSet.Name", dep.Name)
			return ctrl.Result{}, err
		}

		// StatefulSet created successfully at this point.
		// We will requeue the reconciliation so that we can ensure the state
		// and move forward for the next operations
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	} else if err != nil {
		log.Error(err, "Failed to get StatefulSet")
		// Let's return the error for the reconciliation be re-trigged again
		return ctrl.Result{}, err
	}

	//
	// 6. Control-loop: Check the number of replicas
	//
	// The CRD API is defining that the Zookeeper type, have a ZookeeperSpec.Size field
	// to set the quantity of Deployment instances is the desired state on the cluster.
	// Therefore, the following code will ensure the Deployment size is the same as defined
	// via the Size spec of the Custom Resource which we are reconciling.
	size := zookeeper.Spec.Size
	if *found.Spec.Replicas != size {
		found.Spec.Replicas = &size
		if err = r.Update(ctx, found); err != nil {
			log.Error(err, "Failed to update StatefulSet",
				"StatefulSet.Namespace", found.Namespace, "StatefulSet.Name", found.Name)

			// Re-fetch the zookeeper Custom Resource before update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, zookeeper); err != nil {
				log.Error(err, "Failed to re-fetch zookeeper")
				return ctrl.Result{}, err
			}

			// The following implementation will update the status
			meta.SetStatusCondition(&zookeeper.Status.Conditions, metav1.Condition{Type: typeAvailableZookeeper,
				Status: metav1.ConditionFalse, Reason: "Resizing",
				Message: fmt.Sprintf("Failed to update the size for the custom resource (%s): (%s)", zookeeper.Name, err)})

			if err := r.Status().Update(ctx, zookeeper); err != nil {
				log.Error(err, "Failed to update Zookeeper status")
				return ctrl.Result{}, err
			}

			return ctrl.Result{}, err
		}

		// Now, that we update the size we want to requeue the reconciliation
		// so that we can ensure that we have the latest state of the resource before
		// update. Also, it will help ensure the desired state on the cluster
		return ctrl.Result{Requeue: true}, nil
	}

	//
	// 7. Control-loop: Let's update the status
	//
	// The following implementation will update the status
	meta.SetStatusCondition(&zookeeper.Status.Conditions, metav1.Condition{Type: typeAvailableZookeeper,
		Status: metav1.ConditionTrue, Reason: "Reconciling",
		Message: fmt.Sprintf("StatefulSet for custom resource (%s) with %d replicas created successfully", zookeeper.Name, size)})

	if err := r.Status().Update(ctx, zookeeper); err != nil {
		log.Error(err, "Failed to update Zookeeper status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
} // end control-loop function

// finalizeZookeeper will perform the required operations before delete the CR.
func (r *ZookeeperReconciler) doFinalizerOperationsForZookeeper(cr *bigdatav1alpha1.Zookeeper) {
	// TODO(user): Add the cleanup steps that the operator
	// needs to do before the CR can be deleted. Examples
	// of finalizers include performing backups and deleting
	// resources that are not owned by this CR, like a PVC.

	// Note: It is not recommended to use finalizers with the purpose of delete resources which are
	// created and managed in the reconciliation. These ones, such as the Deployment created on this reconcile,
	// are defined as depended of the custom resource. See that we use the method ctrl.SetControllerReference.
	// to set the ownerRef which means that the Deployment will be deleted by the Kubernetes API.
	// More info: https://kubernetes.io/docs/tasks/administer-cluster/use-cascading-deletion/

	// The following implementation will raise an event
	r.Recorder.Event(cr, "Warning", "Deleting",
		fmt.Sprintf("Custom Resource %s is being deleted from the namespace %s",
			cr.Name,
			cr.Namespace))
}

func (r *ZookeeperReconciler) defaultConfigMapForZookeeper(
	v *bigdatav1alpha1.Zookeeper) (*corev1.ConfigMap, error) {

	configMapData := make(map[string]string, 0)
	zkEnv := `
    export ZOOKEEPER__zoocfg__tickTime="2000"
    export ZOOKEEPER__zoocfg__dataDir="/var/lib/zookeeper/data"
    export ZOOKEEPER__zoocfg__dataLogDir="/var/lib/zookeeper/data/log"
    export ZOOKEEPER__zoocfg__confDir="/opt/zookeeper/conf"
    export ZOOKEEPER__zoocfg__clientPort="2181"
    export ZOOKEEPER__zoocfg__serverPort="2888"
    export ZOOKEEPER__zoocfg__electionPort="3888"
    export ZOOKEEPER__zoocfg__initLimit="10"
    export ZOOKEEPER__zoocfg__syncLimit="5"
    export ZOOKEEPER__zoocfg__maxClientCnxns="60"
    export ZOOKEEPER__zoocfg__purgeInterval="12"
    export ZOOKEEPER__zoocfg__adminServerPort="8080"
    export ZOOKEEPER__zoocfg__adminEnableServer="false"
    export ZOOKEEPER__zoocfg__maxSessionTimeout="40000"
    export ZOOKEEPER__zoocfg__minSessionTimeout="4000"
    export ZOOKEEPER__zoocfg__logLevel="INFO"
    export ZOOKEEPER__zoocfg__server_1="zk-0.zk-hs.default.svc.cluster.local:2888:3888"
    export ZOOKEEPER__zoocfg__server_2="zk-1.zk-hs.default.svc.cluster.local:2888:3888"
    export ZOOKEEPER__zoocfg__server_3="zk-2.zk-hs.default.svc.cluster.local:2888:3888"
    export ZOOKEEPER__zoocfg__4lw_commands_whitelist="*"
    export ZOOKEEPER__zoocfg__metricsProvider_className="org.apache.zookeeper.metrics.prometheus.PrometheusMetricsProvider"
    export ZOOKEEPER__zoocfg__metricsProvider_httpPort="7001"
    export ZOOKEEPER__zoojavacfg__ZOO_LOG_DIR="/var/log/zookeeper"
    export ZOOKEEPER__zoojavacfg__JVMFLAGS="-Xmx512M -Xms512M "
    export ZOOKEEPER__zoolog4jcfg__zookeeper_root_logger="CONSOLE"
    export ZOOKEEPER__zoolog4jcfg__log4j_rootLogger="\${zookeeper.root.logger}"
    export ZOOKEEPER__zoolog4jcfg__zookeeper_console_threshold="INFO"
    export ZOOKEEPER__zoolog4jcfg__log4j_appender_CONSOLE="org.apache.log4j.ConsoleAppender"
    export ZOOKEEPER__zoolog4jcfg__log4j_appender_CONSOLE_Threshold="\${zookeeper.console.threshold}"
    export ZOOKEEPER__zoolog4jcfg__log4j_appender_CONSOLE_layout="org.apache.log4j.PatternLayout"
    export ZOOKEEPER__zoolog4jcfg__log4j_appender_CONSOLE_layout_ConversionPattern="%d{ISO8601} [myid:%X{myid}] - %-5p [%t:%C{1}@%L] - %m%n"
	`
	configMapData["zk.env"] = zkEnv
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zk-config",
			Namespace: v.Namespace,
		},
		Data: configMapData,
	}

	if err := ctrl.SetControllerReference(v, configMap, r.Scheme); err != nil {
		return nil, err
	}

	return configMap, nil
}

func (r *ZookeeperReconciler) pdbForZookeeper(
	v *bigdatav1alpha1.Zookeeper) (*v1.PodDisruptionBudget, error) {

	labels := labels(v, "zk")
	maxUnavailable := intstr.FromInt(1)
	pdb := &v1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zk-pdb",
			Labels:    labels,
			Namespace: v.Namespace,
		},
		Spec: v1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			MaxUnavailable: &maxUnavailable,
		},
	}

	if err := ctrl.SetControllerReference(v, pdb, r.Scheme); err != nil {
		return nil, err
	}

	return pdb, nil
}

func (r *ZookeeperReconciler) serviceHeadlessForZookeeper(
	v *bigdatav1alpha1.Zookeeper) (*corev1.Service, error) {

	labels := labels(v, "zk")
	s := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zk-hs",
			Namespace: v.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name: "server",
				Port: 2888,
			},
				{
					Name: "leader-election",
					Port: 3888,
				},
				{
					Name: "metrics-port",
					Port: 7001,
				}},
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None",
		},
	}

	if err := ctrl.SetControllerReference(v, s, r.Scheme); err != nil {
		return nil, err
	}

	return s, nil
}

func (r *ZookeeperReconciler) serviceForZookeeper(
	v *bigdatav1alpha1.Zookeeper) (*corev1.Service, error) {

	labels := labels(v, "zk")
	s := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "zk-cs",
			Namespace: v.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name: "client",
				Port: 2181,
			}},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	if err := ctrl.SetControllerReference(v, s, r.Scheme); err != nil {
		return nil, err
	}

	return s, nil
}

// stateFulSetForZookeeper returns a Zookeeper StateFulSet object
func (r *ZookeeperReconciler) stateFulSetForZookeeper(
	zookeeper *bigdatav1alpha1.Zookeeper) (*appsv1.StatefulSet, error) {

	ls := labelsForZookeeper(zookeeper.Name)
	replicas := zookeeper.Spec.Size

	// Get the Operand image
	image, err := imageForZookeeper()
	if err != nil {
		return nil, err
	}

	fastdisks := "fast-disks"

	dep := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      zookeeper.Name,
			Namespace: zookeeper.Namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: ls,
			},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type:          "RollingUpdate",
				RollingUpdate: nil,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					SecurityContext: &corev1.PodSecurityContext{
						RunAsNonRoot: &[]bool{true}[0],
						// IMPORTANT: seccomProfile was introduced with Kubernetes 1.19
						// If you are looking for to produce solutions to be supported
						// on lower versions you must remove this option.
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{{
						Image:           image,
						Name:            "zookeeper",
						ImagePullPolicy: corev1.PullIfNotPresent,
						// Ensure restrictive context for the container
						// More info: https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot:             &[]bool{true}[0],
							RunAsUser:                &[]int64{1000}[0],
							RunAsGroup:               &[]int64{1000}[0],
							AllowPrivilegeEscalation: &[]bool{true}[0],
							Capabilities: &corev1.Capabilities{
								Drop: []corev1.Capability{
									"ALL",
								},
							},
						},
						Command: []string{"sh", "-c", "start-zookeeper", "--servers=3"},
						Ports: []corev1.ContainerPort{
							{
								ContainerPort: 2181,
								Name:          "client",
							},
							{
								ContainerPort: 2888,
								Name:          "server",
							},
							{
								ContainerPort: 3888,
								Name:          "leader-election",
							},
							{
								ContainerPort: 7001,
								Name:          "metrics-port",
							}},
						VolumeMounts: []corev1.VolumeMount{
							{
								Name:      "datadir",
								MountPath: "/var/lib/zookeeper",
							},
							{
								Name:      "zk-config-volume",
								MountPath: "/etc/environments",
							},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "zk-config-volume",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: "zk-config",
									},
								},
							},
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: "datadir"},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("150Mi"),
						},
					},
					StorageClassName: &fastdisks,
				},
			}},
		},
	}

	// Set the ownerRef for the Deployment
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/
	if err := ctrl.SetControllerReference(zookeeper, dep, r.Scheme); err != nil {
		return nil, err
	}
	return dep, nil
}

// labelsForZookeeper returns the labels for selecting the resources
// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/
func labelsForZookeeper(name string) map[string]string {
	var imageTag string
	image, err := imageForZookeeper()
	if err == nil {
		imageTag = strings.Split(image, ":")[1]
	}
	return map[string]string{"app.kubernetes.io/name": "Zookeeper",
		"app.kubernetes.io/instance":   name,
		"app.kubernetes.io/version":    imageTag,
		"app.kubernetes.io/part-of":    "zookeeper-operator",
		"app.kubernetes.io/created-by": "controller-manager",
	}
}

// imageForZookeeper gets the Operand image which is managed by this controller
// from the ZOOKEEPER_IMAGE environment variable defined in the config/manager/manager.yaml
func imageForZookeeper() (string, error) {
	var imageEnvVar = "ZOOKEEPER_IMAGE"
	image, found := os.LookupEnv(imageEnvVar)
	if !found {
		return "", fmt.Errorf("Unable to find %s environment variable with the image", imageEnvVar)
	}
	return image, nil
}

func labels(v *bigdatav1alpha1.Zookeeper, l string) map[string]string {
	return map[string]string{
		"app":          l,
		"zookeeper_cr": v.Name,
	}
}

// SetupWithManager sets up the controller with the Manager.
// Note that the Deployment will be also watched in order to ensure its
// desirable state on the cluster
func (r *ZookeeperReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&bigdatav1alpha1.Zookeeper{}).
		Owns(&appsv1.StatefulSet{}).
		Complete(r)
}
