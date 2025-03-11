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
	"github.com/prometheus/client_golang/prometheus"
	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	net "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"time"
	"tribefire-operator/common"
	"tribefire-operator/providers"
	"tribefire-operator/tribefire"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	tribefirev1 "tribefire-operator/api/v1"

	. "github.com/thoas/go-funk"
	. "tribefire-operator/common"
)

const (
	requeueAfter         = 5 * time.Second
	maxFinalizerAttempts = 1
)

const (
	PrometheusLabelsErrorOrigin = "origin"
)

const EventTag = "tribefire"
const LogLevelChangeUrlBase = "/api/logging"

const (
	ComponentScaled    = "ComponentScaled"
	EnvironmentChanged = "EnvironmentChanged"
	ImageChanged       = "ImageChanged"
	ResourcesChanged   = "ResourcesChanged"
	LogLevelChanged    = "LogLevelChanged"
)

var SkipEnvVars = []string{"TRIBEFIRE_NODE_ID", "TF_OPERATOR_RANDOM", "DEPLOYMENT_TIMESTAMP", tribefire.RuntimeLogLevel}

var (
	_ reconcile.Reconciler = &TribefireRuntimeReconciler{}
)

//type OkdClients struct {
//	securityClient *securityv1client.SecurityV1Client
//}

// TribefireRuntimeReconciler reconciles a TribefireRuntime object
type TribefireRuntimeReconciler struct {
	Client client.Client
	Cache  cache.Cache
	Scheme *runtime.Scheme

	DbMgr tribefire.TribefireDatabaseMgr

	EventRecorder record.EventRecorder

	EtcdChecker RuntimeStatusChecker

	//okdClients OkdClients
}

//+kubebuilder:rbac:groups=tribefire.cloud,resources=tribefireruntimes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=deployments;daemonsets;replicasets;statefulsets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods;services;endpoints;persistentvolumeclaims;events;configmaps;secrets;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=tribefire.cloud,resources=tribefireruntimes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=tribefire.cloud,resources=tribefireruntimes/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the TribefireRuntime object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.0/pkg/reconcile
func (r *TribefireRuntimeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var err error

	totalReconcileRuns.Inc()

	// Fetch the TribefireRuntime tf
	tf := &tribefirev1.TribefireRuntime{}
	err = r.Client.Get(context.Background(), req.NamespacedName, tf)
	common.SetupLogger(tf.Name, tf.Labels["stage"])

	L().Infof("Reconcile for: '%s'", req.String())
	L().Debugf("Fetched TribefireRuntime: %s", tf.String())

	if err != nil {
		// Request object not found, could have been deleted after reconcile request.
		// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
		// Return and don't requeue
		if errors.IsNotFound(err) {
			L().Debugf("TribefireRuntime %s/%s deleted", req.Namespace, req.Name)

			// todo recording an event at this point does not work since it expects the selfLink to be set which
			// todo isn't the case here anymore since the CR is already deleted
			// r.recordEventEventually(tf, err, "TribefireRuntimeDeleted", message)
			return reconcile.Result{}, nil
		}

		// Error reading the object - requeue the request.
		totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "general"}).Inc()
		L().Errorf("Could not get tf %s/%s: %v", req.Namespace, req.Name, err)
		return reconcile.Result{}, err
	}

	// check if currentGeneration changed by comparing meta.currentGeneration and status.observedGeneration
	observedGeneration := tf.Status.ObservedGeneration
	currentGeneration := tf.Generation

	L().Debugf("Generation: '%d' ObservedGeneration: '%d'", currentGeneration, observedGeneration)

	finalizers := tf.ObjectMeta.Finalizers

	L().Debugf("Finalizers: '%s'", finalizers)

	deletionTimestampZero := tf.ObjectMeta.DeletionTimestamp.IsZero()
	L().Debugf("DeletionTimeStamp: '%s' deletionTimestampZero: '%t'", tf.ObjectMeta.DeletionTimestamp, deletionTimestampZero)

	//////////////// HANDLE DELETE
	//
	// if DeletionTimestamp is not Zero, it means the TribefireRuntime was deleted
	//
	/////////////////////////////////////
	if !deletionTimestampZero {
		// The object is being deleted. If there's still the finalizer attached, cleanup attached resources,
		// remove the finalizer, and re-queue the deletion request
		if ContainsString(finalizers, tribefirev1.DefaultFinalizerName) {
			L().Infof("Handling Delete event for %s", tf)

			r.recordEventEventually(tf, err, "TribefireRuntimeFinalizing", "Finalizing TribefireRuntime")

			for i := maxFinalizerAttempts; i >= 0; i-- {
				// our finalizer is present, so lets handle our external dependency
				err := r.deleteRuntime(tf)
				if err == nil {
					break
				}

				L().Errorf("Error finalizing '%s': %v", tf.String(), err)
			}

			// make sure we use the latest version of the object
			latest, err := r.fetchLatest(tf, false)
			if err != nil {
				totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "resource.query"}).Inc()
				return reconcile.Result{}, err
			}

			// remove our finalizer from the list
			latest.ObjectMeta.Finalizers = FilterString(latest.ObjectMeta.Finalizers, func(finalizer string) bool {
				return finalizer != tribefirev1.DefaultFinalizerName
			})

			message := fmt.Sprintf("TribefireRuntime finalized")
			if err != nil {
				message = fmt.Sprintf("TribefireRuntime finalized with errors: %v", err)
			}

			// update the object
			if err = r.Client.Update(context.Background(), latest); err != nil {
				totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "resource.update"}).Inc()
				return reconcile.Result{}, err
			}

			L().Info(message)
			r.recordEventEventually(latest, err, "TribefireRuntimeFinalized", message)
			return reconcile.Result{}, nil
		}
	}

	//////////////// HANDLE STATUS UPDATE
	//
	// currentGeneration must be > observedGeneration to skip
	//
	/////////////////////////////////////
	if observedGeneration > 0 && (currentGeneration == observedGeneration) && !EnableSameGenerationReconcile() {
		L().Debugf("TribefireRuntime spec did not change on request: '%s' observedGen: '%d' currentGen: '%d'",
			req.String(), observedGeneration, currentGeneration)

		updated, err := r.updateTribefireRuntimeStatus(tf)

		if updated && err == nil {
			L().Debugf("Status updated for '%s'", tf.Name)
		}

		return reconcile.Result{}, err
	}

	//////////////// HANDLE SPEC UPDATE
	//
	// Getting here means the object is _not_ being deleted since the DeletionTimestamp is zero
	//
	///////////////////////////////////
	L().Debugf("DeletionTimestamp is zero for request: '%s' finalizers: '%s' currentGen: '%d'",
		req.String(), tf.ObjectMeta.Finalizers, tf.ObjectMeta.Generation)

	if deletionTimestampZero {
		//
		// note: we're no longer adding the finalizer here since this now happens in the mutating webhook
		//       that sets the defaults
		//
		if !ContainsString(tf.ObjectMeta.Finalizers, tribefirev1.DefaultFinalizerName) {
			L().Infof("New TribefireRuntime %s/%s requested. Adding finalizer and re-queuing.",
				req.Namespace, req.Name)

			tf.ObjectMeta.Finalizers = append(tf.ObjectMeta.Finalizers, tribefirev1.DefaultFinalizerName)
			err = r.Client.Update(context.Background(), tf)
			if err != nil {
				totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "Client.Update"}).Inc()
				L().Errorf("Cannot add finalizer '%s' to tf '%s': %v", tribefirev1.DefaultFinalizerName, tf.String(), err)
				return reconcile.Result{}, err
			}

			return reconcile.Result{}, nil
		}

		// validate initiative and eventually set defaults
		_, err := tribefirev1.SetDefaults(tf)
		if err != nil {
			totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "spec.validate"}).Inc()
		}

		// tf already contains the finalizer, continue with the sync
		err = r.syncRuntime(tf) //todo

		// schedule for another reconcile run if something went wrong, but not immediately
		if err != nil {
			return reconcile.Result{RequeueAfter: requeueAfter}, err
		}

		message := fmt.Sprintf("TribefireRuntime reconciled")
		r.recordEventEventually(tf, err, "TribefireRuntimeReconciled", message)
		L().Info(message)
		return reconcile.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// in case of legacy initiatives that have backend set to ActiveMQ, deploy ActiveMQ
func (r *TribefireRuntimeReconciler) handleComponentMessagingBackend(tf *tribefirev1.TribefireRuntime) error {
	backend := tf.Spec.Backend.Type
	switch backend {
	case tribefirev1.ActiveMqBackend:
		deployment := tribefire.NewActiveMqDeployment(tf)
		err := r.Client.Create(context.Background(), deployment)
		if err != nil && !isAlreadyExistsError(err) {
			L().Errorf("Failed to create ActiveMQ deployment : %v", err)
			return err
		}

		err = r.Client.Create(context.Background(), tribefire.NewActiveMqService(tf))
		if err != nil && !isAlreadyExistsError(err) {
			L().Errorf("Failed to create ActiveMQ service : %v", err)
			return err
		}

		L().Debugf("Using ActiveMQ backend for %s", tf.String())
	case tribefirev1.EtcdBackend:
		L().Debugf("Using etcd backend for %s", tf.String())
		// nothing to do here
	default:
		L().Fatalf("Unsupported backend for tf %s: %s", tf.String(), backend)
	}

	return nil
}

// synchronize initiative
func (r *TribefireRuntimeReconciler) syncRuntime(tf *tribefirev1.TribefireRuntime) error {
	isNew := isInitialSync(tf)
	state := "Initial"
	if !isNew {
		state = "Update"
	}

	L().Debugf("Synchronizing tribefire-runtime %s, state: %s", tf.String(), state)

	// create the "system" database
	dbDesc, err := r.DbMgr.CreateDatabase(tf)
	if err != nil && !isAlreadyExistsError(err) {
		totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "database.create"}).Inc()
		return r.handleSyncRuntimeError(tf, err, tribefirev1.DatabaseBootstrap, "Failed to create database: %v")
	}

	// todo think about whether we should call this every time
	r.recordEventEventually(tf, err, string(tribefirev1.DatabaseBootstrap), "Created database %s", dbDesc.DatabaseName)

	// create the secrets to connect to the system database
	err = r.Client.Create(context.Background(), tribefire.NewDatabaseSecrets(tf, dbDesc))
	if err != nil && !isAlreadyExistsError(err) {
		totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "database.secrets.create"}).Inc()
		return r.handleSyncRuntimeError(tf, err, tribefirev1.SecretBootstrap, "Failed to create database secret: %v")
	}

	r.recordEventEventually(tf, err, string(tribefirev1.SecretBootstrap), "Created database secret")

	// create a secret that holds the service account JSONs for the cloudsql proxy
	if !tf.IsLocalDatabase() {
		secret, err := tribefire.NewServiceAccountSecret(tf)
		if err != nil {
			totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "database.serviceaccount.create"}).Inc()
			return r.handleSyncRuntimeError(tf, err, tribefirev1.SecretBootstrap, "Failed to create service account Secret : %v")
		}

		err = r.Client.Create(context.Background(), secret)
		if err != nil && !isAlreadyExistsError(err) {
			totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "database.serviceaccount.create"}).Inc()
			return r.handleSyncRuntimeError(tf, err, tribefirev1.SecretBootstrap, "Failed to persist service account: %v")
		}

		r.recordEventEventually(tf, err, string(tribefirev1.SecretBootstrap), "Created database service account")
	}

	// create the image pull secrets for artifactory
	err = r.Client.Create(context.Background(), tribefire.NewImagePullSecret(tf))
	if err != nil && !isAlreadyExistsError(err) {
		totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "image.pullsecret.create"}).Inc()
		return r.handleSyncRuntimeError(tf, err, tribefirev1.SecretBootstrap, "Failed to create image pull secret: %v")
	}

	r.recordEventEventually(tf, err, string(tribefirev1.SecretBootstrap), "Created image pull secret")

	// create the RBAC related objects, i.e. service account, role and role binding
	err = r.Client.Create(context.Background(), tribefire.NewServiceAccount(tf))
	if err != nil && !isAlreadyExistsError(err) {
		totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "rbac.serviceaccount.create"}).Inc()
		return r.handleSyncRuntimeError(tf, err, tribefirev1.RbacBootstrap, "Failed to create service account: %v")
	}

	err = r.Client.Create(context.Background(), tribefire.NewRole(tf))
	if err != nil && !isAlreadyExistsError(err) {
		totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "rbac.role.create"}).Inc()
		return r.handleSyncRuntimeError(tf, err, tribefirev1.RbacBootstrap, "Failed to create role: %v")
	}

	err = r.Client.Create(context.Background(), tribefire.NewRoleBinding(tf))
	if err != nil && !isAlreadyExistsError(err) {
		totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "rbac.rolebinding.create"}).Inc()
		return r.handleSyncRuntimeError(tf, err, tribefirev1.RbacBootstrap, "Failed to create role binding: %v")
	}

	r.recordEventEventually(tf, err, string(tribefirev1.RbacBootstrap), "Created RBAC resources")

	// deploy the requested components: master, control-center etc.
	err = r.handleComponentDeployments(tf)
	if err != nil {
		return err
	}

	// check the requested messaging backend and deploy ActiveMQ if requested
	err = r.handleComponentMessagingBackend(tf)
	if err != nil && !isAlreadyExistsError(err) {
		totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "components.messaging.create"}).Inc()
		return r.handleSyncRuntimeError(tf, err, tribefirev1.BackendBootstrap, "Failed to create messaging backend: %v")
	}

	// finally, set the overall initiative state
	_, err = r.updateTribefireRuntimeStatus(tf)
	return err
}

// deploy tribefire master/services
func (r *TribefireRuntimeReconciler) deployMaster(tf *tribefirev1.TribefireRuntime, tfc *tribefirev1.TribefireComponent) error {
	deployment := tribefire.NewTribefireMasterDeployment(tf, tfc)
	err := r.Client.Create(context.Background(), deployment)
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime Master deployment : %v", err)
		return err
	}

	if isAlreadyExistsError(err) {
		L().Debugf("Updating master deployment")
		_, err = r.handleComponentUpdate(tf, tfc, deployment)
	}

	if err != nil {
		L().Errorf("Cannot update master deployment: %v", err)
		return err
	}

	err = r.Client.Create(context.Background(), tribefire.NewTribefireMasterService(tf))
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime Master service : %v", err)
		return err
	}

	ingress := tribefire.NewTribefireMasterIngress(tf, tfc)
	err = r.Client.Create(context.Background(), ingress)
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime Master ingress : %v", err)
		return err
	}

	if isAlreadyExistsError(err) {
		L().Debug("Updating ingress since spec changed")
		err = r.Client.Update(context.Background(), ingress)
	}

	return err
}

// deploy explorer
func (r *TribefireRuntimeReconciler) deployExplorer(tf *tribefirev1.TribefireRuntime, tfc *tribefirev1.TribefireComponent) error {
	deployment := tribefire.NewTribefireExplorerDeployment(tf, tfc)
	err := r.Client.Create(context.Background(), deployment)
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime Explorer deployment : %v", err)
		return err
	}

	if isAlreadyExistsError(err) {
		L().Debugf("Updating explorer deployment")
		_, err = r.handleComponentUpdate(tf, tfc, deployment)
	}

	err = r.Client.Create(context.Background(), tribefire.NewTribefireExplorerService(tf))
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime Explorer service : %v", err)
		return err
	}

	ingress := tribefire.NewTribefireExplorerIngress(tf, tfc)
	err = r.Client.Create(context.Background(), ingress)
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime Explorer ingress : %v", err)
		return err
	}

	if isAlreadyExistsError(err) {
		L().Debug("Updating ingress since spec changed")
		err = r.Client.Update(context.Background(), ingress)
	}

	return err
}

// deploy modeler
func (r *TribefireRuntimeReconciler) deployModeler(tf *tribefirev1.TribefireRuntime, tfc *tribefirev1.TribefireComponent) error {
	deployment := tribefire.NewTribefireModelerDeployment(tf, tfc)
	err := r.Client.Create(context.Background(), deployment)
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime Modeler deployment : %v", err)
		return err
	}

	if isAlreadyExistsError(err) {
		L().Debugf("Updating modeler deployment")
		_, err = r.handleComponentUpdate(tf, tfc, deployment)
	}

	err = r.Client.Create(context.Background(), tribefire.NewTribefireModelerService(tf))
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime Modeler service : %v", err)
		return err
	}

	ingress := tribefire.NewTribefireModelerIngress(tf, tfc)
	err = r.Client.Create(context.Background(), ingress)
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime Modeler ingress : %v", err)
		return err
	}

	if isAlreadyExistsError(err) {
		L().Debug("Updating ingress since spec changed")
		err = r.Client.Update(context.Background(), ingress)
	}

	return err
}

// deploy webreader
func (r *TribefireRuntimeReconciler) deployWebReader(tf *tribefirev1.TribefireRuntime, tfc *tribefirev1.TribefireComponent) error {
	deployment := tribefire.NewTribefireWebReaderDeployment(tf, tfc)
	err := r.Client.Create(context.Background(), deployment)
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime WebReader deployment : %v", err)
		return err
	}

	if isAlreadyExistsError(err) {
		L().Debugf("Updating WebReader deployment")
		_, err = r.handleComponentUpdate(tf, tfc, deployment)
	}

	err = r.Client.Create(context.Background(), tribefire.NewTribefireWebReaderService(tf, tfc))
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime WebReader service : %v", err)
		return err
	}

	ingress := tribefire.NewTribefireWebReaderIngress(tf, tfc)
	err = r.Client.Create(context.Background(), ingress)
	if err != nil && !isAlreadyExistsError(err) {
		L().Errorf("failed to create TribefireRuntime WebReader ingress : %v", err)
		return err
	}

	if isAlreadyExistsError(err) {
		L().Debug("Updating ingress since spec changed")
		err = r.Client.Update(context.Background(), ingress)
	}

	return err
}

// we need to skip certain env var changes, including:
//   - TRIBEFIRE_RUNTIME_LOG_LEVEL
//   - DEPLOYMENT_TIMESTAMP
//   - TRIBEFIRE_NODE_ID
func skipVar(name string) bool {
	return ContainsString(SkipEnvVars, name)
}

// handles a component update, i.e changes to replica count, image/tag, loglevels and resource constraints
// todo this works only for the first container in the PodSpec, might be not what we want for the future
func (r *TribefireRuntimeReconciler) handleComponentUpdate(tf *tribefirev1.TribefireRuntime,
	tfc *tribefirev1.TribefireComponent, newDeployment *apps.Deployment) (bool, error) {

	L().Debugf("Deployment for component %s already exists, updating...", tfc.Name)

	currentDeployment, err := r.queryComponent(tf, tfc)
	if err != nil {
		return false, err
	}

	changed := false

	// handle changes in replica count
	currentReplicas := *currentDeployment.Spec.Replicas
	newReplicas := *newDeployment.Spec.Replicas

	if currentReplicas != newReplicas {
		L().Debugf("Replicas for %s changed from %d to %d",
			tfc.String(), currentReplicas, newReplicas)

		currentDeployment.Spec.Replicas = newDeployment.Spec.Replicas
		r.recordEventEventually(tf, nil, ComponentScaled, "Component %s scaled to %d replicas",
			tfc.Name, tfc.Replicas)

		changed = true
	}

	// handle changes of resource constraints
	currentResourceLimits := currentDeployment.Spec.Template.Spec.Containers[0].Resources.Limits
	newResourceLimits := newDeployment.Spec.Template.Spec.Containers[0].Resources.Limits

	currentResourceRequests := currentDeployment.Spec.Template.Spec.Containers[0].Resources.Requests
	newResourceRequests := newDeployment.Spec.Template.Spec.Containers[0].Resources.Requests

	cpuLimitsChanged := currentResourceLimits.Cpu().Value() != newResourceLimits.Cpu().Value()
	memLimitsChanged := currentResourceLimits.Memory().Value() != newResourceLimits.Memory().Value()

	cpuRequestsChanged := currentResourceRequests.Cpu().Value() != newResourceRequests.Cpu().Value()
	memRequestsChanged := currentResourceRequests.Memory().Value() != newResourceRequests.Memory().Value()

	if cpuLimitsChanged || memLimitsChanged || cpuRequestsChanged || memRequestsChanged {
		L().Debugf("Limits and/or requests changed for %s ", tfc.String())
		L().Debugf("Cpu Requests: old %s new %s", currentResourceRequests.Cpu().String(), newResourceRequests.Cpu().String())
		L().Debugf("Mem Requests: old %s new %s", currentResourceRequests.Memory().String(), newResourceRequests.Memory().String())
		L().Debugf("Cpu Limits: old %s new %s", currentResourceLimits.Cpu().String(), newResourceLimits.Cpu().String())
		L().Debugf("Mem Limits: old %s new %s", currentResourceLimits.Memory().String(), newResourceLimits.Memory().String())
		currentDeployment.Spec.Template.Spec.Containers[0].Resources.Limits = newResourceLimits
		currentDeployment.Spec.Template.Spec.Containers[0].Resources.Requests = newResourceRequests

		r.recordEventEventually(tf, nil, ResourcesChanged, "Resource constraints for %s changed", tfc.Name)
		changed = true
	}

	// handle image name/tag change
	currentImage := currentDeployment.Spec.Template.Spec.Containers[0].Image
	newImage := newDeployment.Spec.Template.Spec.Containers[0].Image

	if currentImage != newImage {
		L().Debugf("Image for component %s changed from %s to %s", tfc.Name, currentImage, newImage)
		currentDeployment.Spec.Template.Spec.Containers[0].Image = newImage
		r.recordEventEventually(tf, nil, ImageChanged, "Image for %s changed", tfc.Name)
		changed = true
	}

	// handle environment changes
	currentEnv := currentDeployment.Spec.Template.Spec.Containers[0].Env
	newEnv := newDeployment.Spec.Template.Spec.Containers[0].Env
	for _, envVar := range newEnv {
		newValue := envVar.Value

		// skip the log level env var, otherwise it would show up twice in the events
		if skipVar(envVar.Name) {
			continue
		}

		currentVar := tribefire.FindEnvVar(currentEnv, envVar.Name)
		if currentVar == nil {
			L().Debugf("New env var detected for component %s: %s -> %s",
				tfc.Name, envVar.Name, envVar.Value)

			currentEnv = tribefire.UpdateEnvVar(currentEnv, envVar.Name, envVar.Value)
			currentDeployment.Spec.Template.Spec.Containers[0].Env = currentEnv
			r.recordEventEventually(tf, nil, EnvironmentChanged, "Environment for %s changed. New var: %s",
				tfc.Name, envVar.Name)

			changed = true
		}

		if currentVar != nil && newValue != currentVar.Value {
			L().Debugf("Env var change detected for component %s: Name=%s Old=%s New=%s",
				tfc.Name, envVar.Name, currentVar.Value, envVar.Value)

			currentEnv = tribefire.UpdateEnvVar(currentEnv, envVar.Name, envVar.Value)
			currentDeployment.Spec.Template.Spec.Containers[0].Env = currentEnv
			r.recordEventEventually(tf, nil, EnvironmentChanged, "Environment for %s changed. Updated var: %s",
				tfc.Name, envVar.Name)

			changed = true
		}
	}

	// update log-level
	currentLogLevel := tribefire.FindEnvVar(currentEnv, tribefire.RuntimeLogLevel)
	newLogLevel := tfc.LogLevel

	if currentLogLevel == nil {
		L().Errorf("No log level env var found for component %s, inconsistent state!", tfc.Name)
	}

	// we don't set "changed" here since we don't want to redeploy the runtime
	if currentLogLevel != nil && newLogLevel != currentLogLevel.Value {
		L().Debugf("Log level changed for component %s. Old=%s New=%s",
			tfc.Name, currentLogLevel.Value, newLogLevel)

		r.recordEventEventually(tf, nil, LogLevelChanged, "Log level for %s changed to %s",
			tfc.Name, tfc.LogLevel)

		// todo enable actual change here
		//err = changeLogLevel(tf, tfc)
		envVars := currentDeployment.Spec.Template.Spec.Containers[0].Env
		envVars = tribefire.UpdateEnvVar(envVars, tribefire.RuntimeLogLevel, newLogLevel)

		currentDeployment.Spec.Template.Spec.Containers[0].Env = envVars
		changed = true // todo remove in the future
	}

	// if something changed, update the deployment and force a restart
	if changed {
		L().Debugf("Updating deployment for component: %s", tfc.Name)
		err = r.Client.Update(context.Background(), currentDeployment)
	}

	return changed, err
}

// switch on the list of initiative components and deploy each component individually
func (r *TribefireRuntimeReconciler) handleComponentDeployments(tf *tribefirev1.TribefireRuntime) error {
	for _, component := range tf.Spec.Components {

		componentType := tribefirev1.ComponentType(component.Type)
		switch componentType {

		case tribefirev1.Services:
			err := r.deployMaster(tf, &component)
			if err != nil && !isAlreadyExistsError(err) {
				totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "components.master.create"}).Inc()
				return r.handleSyncRuntimeError(tf, err, tribefirev1.ComponentDeployment, "Failed to create tribefire-master: %v")
			}

			r.recordEventEventually(tf, err, string(tribefirev1.ComponentDeployment), "Created tribefire-master")

		//case tribefirev1.ControlCenter:
		//	err := r.deployControlCenter(tf, &component)
		//	if err != nil && !isAlreadyExistsError(err) {
		//		totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "components.controlcenter.create"}).Inc()
		//		return r.handleSyncRuntimeError(tf, err, tribefirev1.ComponentDeployment, "Failed to create control-center: %v")
		//	}
		//
		//	r.recordEventEventually(tf, err, string(tribefirev1.ComponentDeployment), "Created tribefire-control-center")

		case tribefirev1.Explorer:
			err := r.deployExplorer(tf, &component)
			if err != nil && !isAlreadyExistsError(err) {
				return r.handleSyncRuntimeError(tf, err, tribefirev1.ComponentDeployment, "Failed to create explorer: %v")
			}

			r.recordEventEventually(tf, err, string(tribefirev1.ComponentDeployment), "Created tribefire-explorer")

		case tribefirev1.Modeler:
			err := r.deployModeler(tf, &component)
			if err != nil && !isAlreadyExistsError(err) {
				return r.handleSyncRuntimeError(tf, err, tribefirev1.ComponentDeployment, "Failed to create modeler: %v")
			}

			r.recordEventEventually(tf, err, string(tribefirev1.ComponentDeployment), "Created tribefire-modeler")

		case tribefirev1.WebReader:
			err := r.deployWebReader(tf, &component)
			if err != nil && !isAlreadyExistsError(err) {
				return r.handleSyncRuntimeError(tf, err, tribefirev1.ComponentDeployment, "Failed to create webreader: %v")
			}

			r.recordEventEventually(tf, err, string(tribefirev1.ComponentDeployment), "Created webreader")
		}
	}

	return nil
}

// logs the error and emits a corresponding event using the EventRecorder
func (r *TribefireRuntimeReconciler) handleSyncRuntimeError(
	tf *tribefirev1.TribefireRuntime, err error, reason tribefirev1.DeploymentPhase, message string) error {

	L().Errorf(message, err)
	r.recordEventEventually(tf, err, string(reason), message, err)
	return err
}

// custom delete/finalize things
func (r *TribefireRuntimeReconciler) deleteRuntime(tf *tribefirev1.TribefireRuntime) error {

	// delete the "system" database AND the master deployment since this might have open
	// connections to the CloudSQL instance
	// the rest of the resources will be automatically removed because of the ownerRef
	// todo this is probably not needed anymore
	//if !tf.IsLocalDatabase() {
	//
	//	masterDeployment := &apps.Deployment{
	//		TypeMeta: meta.TypeMeta{
	//			Kind:       "Deployment",
	//			APIVersion: "apps/v1",
	//		},
	//		ObjectMeta: meta.ObjectMeta{
	//			Name:      tribefire.DefaultResourceName(tf, tribefire.MasterAppName),
	//			Namespace: tf.Namespace,
	//		},
	//	}
	//
	//	options := client.PropagationPolicy(meta.DeletePropagationForeground)
	//	err := r.Client.Delete(context.Background(), masterDeployment, options)
	//	if err != nil {
	//		L().Errorf("Foreground deletion of master deployment failed: %v", err)
	//	}
	//
	//	err = r.DbMgr.DeleteDatabase(tf)
	//	if err != nil {
	//		totalReconcileErrors.With(prometheus.Labels{PrometheusLabelsErrorOrigin: "database.delete"}).Inc()
	//		L().Errorf("failed to delete database resource: %v", err)
	//		return err
	//	}
	//}

	return nil
}

// check annotation to decide if this is the first time sync
func isInitialSync(tf *tribefirev1.TribefireRuntime) bool {
	return tf.Status.Created == ""
}

// fetch latest version of TribefireRuntime and merge with given data
func (r *TribefireRuntimeReconciler) fetchLatest(tf *tribefirev1.TribefireRuntime, merge bool) (*tribefirev1.TribefireRuntime, error) {
	objectKey := client.ObjectKey{Name: tf.Name, Namespace: tf.Namespace}
	updatedRuntime := &tribefirev1.TribefireRuntime{}
	if merge {
		updatedRuntime = tf.DeepCopy()
	}

	err := r.Client.Get(context.Background(), objectKey, updatedRuntime)
	if err != nil {
		L().Errorf("Cannot get latest version of tribefire-runtime %s: %v", tf.String(), err)
		return nil, err
	}

	return updatedRuntime, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TribefireRuntimeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Cache = mgr.GetCache()
	return ctrl.NewControllerManagedBy(mgr).
		For(&tribefirev1.TribefireRuntime{}).
		Owns(&apps.Deployment{}).
		Owns(&net.Ingress{}).
		Complete(r)
}

// check if given error is some kind of already exists error
func isAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}

	if errors.IsAlreadyExists(err) {
		return true
	}

	if err == providers.DatabaseAlreadyExists || err == providers.UserAlreadyExist {
		return true
	}

	return false
}

// emit an event for this TribefireRuntime iff given err != AlreadyExists (for reconcile cases).
// Otherwise we would have duplicate "Created XXX" events in the history
func (r *TribefireRuntimeReconciler) recordEventEventually(
	tf *tribefirev1.TribefireRuntime, err error, reason string, message string, args ...interface{}) {

	if isAlreadyExistsError(err) {
		return
	}

	eventType := v1.EventTypeNormal
	if err != nil {
		eventType = v1.EventTypeWarning
	}

	if len(args) > 0 {
		r.EventRecorder.Eventf(tf, eventType, reason, message, args...)
		return
	}

	r.EventRecorder.Event(tf, eventType, reason, message)
}

func (r *TribefireRuntimeReconciler) addSecondaryResources(c controller.Controller) error {
	// Watch for changes to these Kubernetes resources
	watchTypes := []client.Object{&apps.Deployment{}, &net.Ingress{}}

	var err error
	for _, watchType := range watchTypes {
		handler := handler.EnqueueRequestForOwner(
			r.Client.Scheme(),
			r.Client.RESTMapper(),
			&tribefirev1.TribefireRuntime{},
			handler.OnlyControllerOwner(),
		)

		err = c.Watch(
			source.Kind(
				r.Cache,
				watchType,
				handler,
			),
		)

		watchName := reflect.TypeOf(watchType).String()
		if err != nil {
			L().Errorf("Cannot start watching %s resources: %v", watchName, err)
			return err
		} else {
			L().Debugf("Started watching %s resources", watchName)
		}
	}

	return err
}
