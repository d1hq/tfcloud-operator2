package controllers

import (
	"context"
	"fmt"
	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	net "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"testing"
	"time"
	tribefirev1 "tribefire-operator/api/v1"
	"tribefire-operator/providers"
	"tribefire-operator/tribefire"
)

type MockReader struct {
	mock.Mock
}

type MockWriter struct {
	mock.Mock
}

type MockStatusClient struct {
	mock.Mock
}

type MockStatusWriter struct {
	mock.Mock
}

type MockEventRecorder struct {
	mock.Mock
}

type MockDatabaseProvider struct {
	mock.Mock
}

type MockEtcdChecker struct {
	mock.Mock
}

const (
	runtimeNamespace                     = "tribefire"
	runtimeNameNotExists                 = "not-exists"
	runtimeNameTestUpdate                = "test-update"
	runtimeNameTestUpdateStatusAvailable = "test-update-status-available"
	runtimeNameTestInitial               = "test-initial"
	runtimeNameTestPristine              = "test-pristine"
	runtimeNameTestDeleteLocal           = "test-delete-local"
	runtimeNameTestDeleteCloud           = "test-delete-cloud"

	tribefireMasterDeploymentAvailable = "test-update-tribefire-master"
)

var groupResource = schema.GroupResource{Group: "tribefire.cloud", Resource: "TribefireRuntime"}

var validDeploymentNameSuffixes = []string{
	tribefire.MasterAppName,
	tribefire.ControlCenterAppName,
	tribefire.ExplorerAppName,
	tribefire.ModelerAppName,
}

//
//
// mock implementations
//
//

// client mock
// func (m *MockReader) Get(ctx context.Context, key client.ObjectKey, obj runtime.Object, options ...client.GetOption) error {
func (m *MockReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {

	args := m.Called(ctx, key, obj)

	switch t := obj.(type) {
	case *tribefirev1.TribefireRuntime:
		{

			// pristine runtime, no finalizer set, has Generation 0
			if key.Name == runtimeNameTestPristine {
				testRuntime := buildTribefireRuntime(runtimeNameTestPristine, runtimeNamespace)
				testRuntime.DeepCopyInto(t)
			}

			// runtime already exists (and has finalizer set) so set Generation to > 0
			if key.Name == runtimeNameTestInitial {
				testRuntime := buildTribefireRuntime(runtimeNameTestInitial, runtimeNamespace)
				testRuntime.Generation = 2
				testRuntime.Status.ObservedGeneration = 1
				testRuntime.Finalizers = append(testRuntime.Finalizers, tribefirev1.DefaultFinalizerName)
				testRuntime.DeepCopyInto(t)
			}

			// runtime already had initial sync, so Generation == 2 and sync timestamps are set
			if key.Name == runtimeNameTestUpdate {
				testRuntime := buildTribefireRuntime(runtimeNameTestUpdate, runtimeNamespace)
				setCommonRuntimeFields(testRuntime)
				testRuntime.DeepCopyInto(t)
			}

			// runtime already had initial sync, so Generation == 2 and sync timestamps are set
			// used for testing updateTribefireRuntimeStatus()
			if key.Name == runtimeNameTestUpdateStatusAvailable {
				testRuntime := buildTribefireRuntime(runtimeNameTestUpdateStatusAvailable, runtimeNamespace)
				setCommonRuntimeFields(testRuntime)
				testRuntime.DeepCopyInto(t)
				t.Status.Message = string(tribefirev1.Available)
			}

			// runtime deleted, so Generation changed and DeletionTimestamp is set
			if key.Name == runtimeNameTestDeleteLocal {
				testRuntime := buildRuntimeForDeleteTests(runtimeNameTestDeleteLocal)
				testRuntime.Spec.DatabaseType = tribefirev1.LocalPostgresql
				testRuntime.DeepCopyInto(t)
			}

			// runtime deleted, so Generation changed and DeletionTimestamp is set
			if key.Name == runtimeNameTestDeleteCloud {
				testRuntime := buildRuntimeForDeleteTests(runtimeNameTestDeleteCloud)
				testRuntime.Spec.DatabaseType = tribefirev1.CloudSqlDatabase
				testRuntime.DeepCopyInto(t)
			}
		}
	case *apps.Deployment:
		{
			if key.Name == tribefireMasterDeploymentAvailable {
				t.Name = key.Name
				t.Namespace = key.Namespace
				t.Status.Replicas = 1
				t.Status.AvailableReplicas = 1
				t.Status.UnavailableReplicas = 0
			}

		}
	}

	return args.Error(0)
}

func buildRuntimeForDeleteTests(name string) *tribefirev1.TribefireRuntime {
	testRuntime := buildTribefireRuntime(name, runtimeNamespace)
	setCommonRuntimeFields(testRuntime)
	deletionTimestamp := metav1.NewTime(time.Now())
	testRuntime.DeletionTimestamp = &deletionTimestamp
	testRuntime.Generation = 3
	return testRuntime
}

func setCommonRuntimeFields(testRuntime *tribefirev1.TribefireRuntime) {
	testRuntime.Generation = 2
	testRuntime.Status.ObservedGeneration = 2
	syncTime := addFirstTimeSyncAnnotationIfInitial(testRuntime)
	updateLastSyncTimeAnnotation(testRuntime, syncTime)
	testRuntime.Finalizers = append(testRuntime.Finalizers, tribefirev1.DefaultFinalizerName)
}

func (m *MockReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	args := m.Called(ctx, opts, list)
	return args.Error(0)
}

func (m *MockStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	args := m.Called(ctx, obj)
	return args.Error(0)
}

// Add Patch method
func (m *MockStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	args := m.Called(ctx, obj, patch)
	return args.Error(0)
}

// Add Create method
func (m *MockStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	args := m.Called(ctx, obj, subResource)
	return args.Error(0)
}

// Add Get method if needed
func (m *MockStatusWriter) Get(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceGetOption) error {
	args := m.Called(ctx, obj, subResource)
	return args.Error(0)
}

func (m *MockWriter) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	args := m.Called(ctx, obj)
	return args.Error(0)
}

func (m *MockWriter) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	args := m.Called(ctx, obj)
	return args.Error(0)
}

func (m *MockWriter) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	args := m.Called(ctx, obj)
	return args.Error(0)
}

func (m *MockWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	args := m.Called(ctx, obj)
	return args.Error(0)
}

func (m *MockWriter) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	args := m.Called(ctx, obj)
	return args.Error(0)
}

func (m *MockStatusClient) Status() client.StatusWriter {
	args := m.Called()
	return args.Get(0).(client.StatusWriter)
}

// EventRecorder mock
func (m *MockEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	_ = m.Called(object, eventtype, reason, message)
}

func (m *MockEventRecorder) Eventf(object runtime.Object, eventtype, reason, messageFmt string, args ...interface{}) {
	_ = m.Called(object, eventtype, reason, messageFmt, args)
}

func (m *MockEventRecorder) PastEventf(object runtime.Object, timestamp metav1.Time, eventtype, reason, messageFmt string, args ...interface{}) {
	_ = m.Called(object, timestamp, eventtype, reason, messageFmt, args)
}

func (m *MockEventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventtype, reason, messageFmt string, args ...interface{}) {
	_ = m.Called(object, annotations, eventtype, reason, messageFmt, args)
}

// database provider mock
func (m *MockDatabaseProvider) CreateDatabase(tf *tribefirev1.TribefireRuntime) (*providers.DatabaseDescriptor, error) {
	args := m.Called(tf)
	return args.Get(0).(*providers.DatabaseDescriptor), args.Error(1)
}

func (m *MockDatabaseProvider) DeleteDatabase(tf *tribefirev1.TribefireRuntime) error {
	args := m.Called(tf)
	return args.Error(0)
}

//func (m *MockDatabaseProvider) CreateDatabase(desc *providers.DatabaseDescriptor) (*providers.DatabaseDescriptor, error) {
//	m.Called(desc)
//	return nil, nil
//}
//func (m *MockDatabaseProvider) DeleteDatabase(desc *providers.DatabaseDescriptor) error {
//	m.Called(desc)
//	return nil
//}
//func (m *MockDatabaseProvider) RetrieveDatabase(desc *providers.DatabaseDescriptor) (*providers.DatabaseDescriptor, error) {
//	m.Called(desc)
//	return nil, nil
//}
//
//func (m *MockDatabaseProvider) CreateUser(desc *providers.DatabaseDescriptor) (*providers.DatabaseDescriptor, error) {
//	m.Called(desc)
//	return nil, nil
//}
//
//func (m *MockDatabaseProvider) DeleteUser(desc *providers.DatabaseDescriptor) error {
//	m.Called(desc)
//	return nil
//}
//
//func (m *MockDatabaseProvider) RetrieveUser(desc *providers.DatabaseDescriptor) (*providers.DatabaseDescriptor, error) {
//	m.Called(desc)
//	return nil, nil
//}

//
// etcd checker mock
//

func (m *MockEtcdChecker) Check(tf *tribefirev1.TribefireRuntime) error {
	args := m.Called(tf)
	return args.Error(0)
}

//
//
// test methods
//
//

func TestClientGetNonExisting(t *testing.T) {
	mockReader, testRuntime, key := setupMockReader(runtimeNameNotExists)

	mockReader.
		On("Get", mock.Anything, key, testRuntime).
		Return(k8serr.NewNotFound(groupResource, runtimeNameNotExists))

	err := mockReader.Get(context.Background(), key, testRuntime)
	assert.True(t, k8serr.IsNotFound(err))
	mockReader.AssertExpectations(t)
}

func TestClientGetPristine(t *testing.T) {
	mockReader, testRuntime, key := setupMockReader(runtimeNameTestPristine)

	mockReader.On("Get", context.Background(), key, mock.Anything).Return(nil)

	err := mockReader.Get(context.Background(), key, testRuntime)

	assert.Nil(t, err)
	assert.Equal(t, runtimeNameTestPristine, testRuntime.Name)
	assert.Equal(t, runtimeNamespace, testRuntime.Namespace)
	assert.Equal(t, int64(0), testRuntime.Generation)

	mockReader.AssertExpectations(t)
}

func TestClientGetInitial(t *testing.T) {
	mockReader, testRuntime, key := setupMockReader(runtimeNameTestInitial)

	mockReader.On("Get", context.Background(), key, mock.Anything).Return(nil)

	err := mockReader.Get(context.Background(), key, testRuntime)

	assert.Nil(t, err)
	assert.Equal(t, runtimeNameTestInitial, testRuntime.Name)
	assert.Equal(t, runtimeNamespace, testRuntime.Namespace)
	assert.Equal(t, int64(2), testRuntime.Generation)
	assert.Equal(t, int64(1), testRuntime.Status.ObservedGeneration)
	assert.Contains(t, testRuntime.Finalizers, tribefirev1.DefaultFinalizerName, "Default finalizer not present")
	assert.Equal(t, "", testRuntime.Status.Created)
	assert.Equal(t, "", testRuntime.Status.Updated)

	mockReader.AssertExpectations(t)
}

func TestClientGetUpdate(t *testing.T) {
	mockReader, testRuntime, key := setupMockReader(runtimeNameTestUpdate)

	mockReader.On("Get", context.Background(), key, mock.Anything).Return(nil)

	err := mockReader.Get(context.Background(), key, testRuntime)

	assert.Nil(t, err)
	assert.Equal(t, runtimeNameTestUpdate, testRuntime.Name)
	assertUpdateRuntimeFields(t, testRuntime)

	mockReader.AssertExpectations(t)
}

func TestClientGetDeleted(t *testing.T) {
	mockReader, testRuntime, key := setupMockReader(runtimeNameTestDeleteLocal)

	mockReader.On("Get", context.Background(), key, mock.Anything).Return(nil)

	err := mockReader.Get(context.Background(), key, testRuntime)

	assert.Nil(t, err)
	assert.Equal(t, runtimeNameTestDeleteLocal, testRuntime.Name)
	assertUpdateRuntimeFields(t, testRuntime)
	assert.NotEqual(t, "", testRuntime.DeletionTimestamp)

	mockReader.AssertExpectations(t)
}

func setupMockReader(name string) (*MockReader, *tribefirev1.TribefireRuntime, client.ObjectKey) {
	mockReader := new(MockReader)
	testRuntime := &tribefirev1.TribefireRuntime{}
	key := objectKey(name)
	return mockReader, testRuntime, key
}

func assertUpdateRuntimeFields(t *testing.T, testRuntime *tribefirev1.TribefireRuntime) {
	assert.Equal(t, runtimeNamespace, testRuntime.Namespace)
	expectedGeneration := 2
	testingDelete := testRuntime.Name == runtimeNameTestDeleteLocal || testRuntime.Name == runtimeNameTestDeleteCloud
	if testingDelete {
		expectedGeneration = 3
	}

	assert.Equal(t, int64(expectedGeneration), testRuntime.Generation)
	assert.Contains(t, testRuntime.Finalizers, tribefirev1.DefaultFinalizerName, "Default finalizer not present")

	_, err := time.Parse(time.RFC3339, testRuntime.Status.Created)
	assert.NoError(t, err, "Cannot parse Created timestamp: %v", err)

	_, err = time.Parse(time.RFC3339, testRuntime.Status.Updated)
	assert.NoError(t, err, "Cannot parse Updated timestamp: %v", err)
}

// test the case where the TribefireRuntime is already gone
func TestReconcileForNonExistingRuntime(t *testing.T) {
	mockReader, _, key := setupMockReader(runtimeNameNotExists)

	mockReader.
		On("Get", mock.Anything, key, mock.Anything).
		Return(k8serr.NewNotFound(groupResource, runtimeNameTestUpdate))

	r := createReconcilerWithMockedClients(mockReader, nil, nil, nil, nil)

	request := reconcile.Request{NamespacedName: objectKey(runtimeNameNotExists)}
	result, err := r.Reconcile(context.TODO(), request)
	assert.Nil(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	mockReader.AssertExpectations(t)
}

// test the case where the TribefireRuntime is already gone
func TestReconcileUnexpectedErrorDuringGet(t *testing.T) {
	mockReader, _, key := setupMockReader(runtimeNameNotExists)

	mockReader.
		On("Get", mock.Anything, key, mock.Anything).
		Return(k8serr.NewBadRequest("bad request"))

	r := createReconcilerWithMockedClients(mockReader, nil, nil, nil, nil)

	request := reconcile.Request{NamespacedName: objectKey(runtimeNameNotExists)}
	result, err := r.Reconcile(context.TODO(), request)
	assert.NotNil(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	mockReader.AssertExpectations(t)
}

// test the case where the Runtime spec did not change, we need to update the status
// but the status update is not persisted
func TestReconcileRuntimeSpecNoChangeStatusUpdateNotPersists(t *testing.T) {
	//mockReader := new(MockReader)
	mockReader, _, _ := setupMockReader(runtimeNameTestUpdate)
	mockStatusWriter := new(MockStatusWriter)
	mockStatusClient := new(MockStatusClient)
	mockEtcdChecker := new(MockEtcdChecker)

	mockReader.
		On("Get",
			context.Background(),
			mock.MatchedBy(buildObjectKeyNameMatcher(runtimeNameTestUpdate)),
			mock.Anything).
		Return(nil)

	mockStatusClient.On("Status").Return(mockStatusWriter)
	mockStatusWriter.On("Update", mock.Anything, mock.Anything).Return(nil)

	mockEtcdChecker.On("Check", mock.Anything).Return(nil)

	r := createReconcilerWithMockedClients(mockReader,
		nil, mockStatusClient, nil, nil)

	r.EtcdChecker = mockEtcdChecker

	CacheSettleRetries = 1
	request := reconcile.Request{NamespacedName: objectKey(runtimeNameTestUpdate)}
	result, err := r.Reconcile(context.TODO(), request)
	assert.Equal(t, tribefirev1.StatusUpdateNotPeristedError, err)
	assert.Equal(t, reconcile.Result{}, result)
	mockReader.AssertExpectations(t)
	CacheSettleRetries = 0
}

// test the case where the Runtime spec did not change, we need to update the status
// but the status update returns an error
func TestReconcileRuntimeSpecNoChangeStatusUpdateError(t *testing.T) {
	mockReader := new(MockReader)
	mockStatusWriter := new(MockStatusWriter)
	mockStatusClient := new(MockStatusClient)
	mockEtcdChecker := new(MockEtcdChecker)

	updateErr := k8serr.NewUnauthorized("unauthorized")

	mockStatusClient.On("Status").Return(mockStatusWriter)
	mockStatusWriter.On("Update", mock.Anything, mock.Anything).Return(updateErr)

	mockEtcdChecker.On("Check", mock.Anything).Return(nil)

	mockReader.
		On("Get",
			context.Background(),
			mock.MatchedBy(buildObjectKeyNameMatcher(runtimeNameTestUpdate)),
			mock.Anything).
		Return(nil)

	r := createReconcilerWithMockedClients(mockReader, nil, mockStatusClient, nil, nil)

	r.EtcdChecker = mockEtcdChecker

	request := reconcile.Request{NamespacedName: objectKey(runtimeNameTestUpdate)}

	// speedup the testcase by limiting to 1 retry
	//CacheSettleRetries = 1

	result, err := r.Reconcile(context.TODO(), request)
	assert.Equal(t, updateErr, err)
	assert.Equal(t, reconcile.Result{}, result)
	mockReader.AssertExpectations(t)
}

// test the case where the Runtime spec did not change, we need to update the status
// and all is fine
func TestReconcileRuntimeSpecNoChangeStatusUpdateOk(t *testing.T) {
	mockReader := new(MockReader)
	mockStatusWriter := new(MockStatusWriter)
	mockStatusClient := new(MockStatusClient)
	mockEtcdChecker := new(MockEtcdChecker)

	mockStatusClient.On("Status").Return(mockStatusWriter)

	mockStatusWriter.On("Update", mock.Anything, mock.Anything).Return(nil)
	mockStatusWriter.On("Create", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockStatusWriter.On("Patch", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	mockStatusWriter.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	mockEtcdChecker.On("Check", mock.Anything).Return(nil)

	mockReader.
		On("Get",
			context.Background(),
			mock.MatchedBy(buildObjectKeyNameMatcher(runtimeNameTestUpdateStatusAvailable)), mock.Anything).
		Return(nil)

	r := createReconcilerWithMockedClients(mockReader, nil, mockStatusClient, nil, nil)
	r.EtcdChecker = mockEtcdChecker

	request := reconcile.Request{NamespacedName: objectKey(runtimeNameTestUpdateStatusAvailable)}

	result, err := r.Reconcile(context.TODO(), request)
	assert.Nil(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	mockReader.AssertExpectations(t)
}

// test the case where the Runtime spec did not change, we need to update the status
// but the status update returns an error
func TestReconcileRuntimeAddFinalizerAndRequeue(t *testing.T) {
	mockReader, _, key := setupMockReader(runtimeNameTestPristine)
	mockWriter := new(MockWriter)

	finalizerMatcher := mock.MatchedBy(func(tf *tribefirev1.TribefireRuntime) bool {
		finalizerOk := tf.Finalizers[0] == tribefirev1.DefaultFinalizerName
		nameOk := tf.Name == runtimeNameTestPristine
		statusOk := tf.Status.Created == "" && tf.Status.Updated == ""
		return finalizerOk && nameOk && statusOk
	})

	mockReader.On("Get", context.Background(), key, mock.Anything).Return(nil)

	mockWriter.
		On("Update", mock.Anything, finalizerMatcher).
		Return(nil)

	r := createReconcilerWithMockedClients(mockReader, mockWriter, nil, nil, nil)
	request := reconcile.Request{NamespacedName: objectKey(runtimeNameTestPristine)}

	result, err := r.Reconcile(context.TODO(), request)
	assert.Nil(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	mockReader.AssertExpectations(t)
}

// test the case where reconcile handles pristine runtime, tries to add the Finalizer client.Update()
// returns an error
func TestReconcileRuntimeAddFinalizerUpdateError(t *testing.T) {
	mockReader, _, key := setupMockReader(runtimeNameTestPristine)
	mockWriter := new(MockWriter)

	finalizerMatcher := mock.MatchedBy(func(tf *tribefirev1.TribefireRuntime) bool {
		return tf.Finalizers[0] == tribefirev1.DefaultFinalizerName
	})

	mockReader.On("Get", context.Background(), key, mock.Anything).Return(nil)

	mockWriter.
		On("Update", mock.Anything, finalizerMatcher).
		Return(k8serr.NewInternalError(errors.New("Something really bad happened")))

	r := createReconcilerWithMockedClients(mockReader, mockWriter, nil, nil, nil)
	request := reconcile.Request{NamespacedName: objectKey(runtimeNameTestPristine)}

	result, err := r.Reconcile(context.TODO(), request)
	assert.NotNil(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	mockReader.AssertExpectations(t)
}

// test the case where the finalizer is already there and a "real" reconcile is triggered, i.e. r.syncRuntime() is called
func TestReconcileRuntimeSyncRuntimeSimpleDeployment(t *testing.T) {
	mockReader := new(MockReader)
	mockWriter := new(MockWriter)
	mockStatusWriter := new(MockStatusWriter)
	mockStatusClient := new(MockStatusClient)
	mockEtcdChecker := new(MockEtcdChecker)

	mockStatusClient.On("Status").Return(mockStatusWriter)

	// prepare database mock
	dbMgrMock := MockDatabaseProvider{}
	dbDesc := providers.DatabaseDescriptor{
		DatabaseName:     "test",
		DatabaseUser:     "user",
		DatabasePassword: "password",
	}

	dbMgrMock.On("CreateDatabase", mock.Anything).Return(&dbDesc, nil)

	// prepare event recorder mock
	mockedEventRecorder := &MockEventRecorder{}

	// check for event message after database bootstrapping
	mockedEventRecorder.
		On("Eventf", mock.Anything, "Normal", "DatabaseBootstrap", "Created database %s", []interface{}{dbDesc.DatabaseName})

	// check for event message after secrets bootstrapping
	mockedEventRecorder.
		On("Event", mock.Anything, "Normal", "SecretBootstrap", "Created database secret")

	// check for event message after service account was bootstrapped
	mockedEventRecorder.
		On("Event", mock.Anything, "Normal", "SecretBootstrap", "Created database service account")

	// check for event message after image pull secret was bootstrapped
	mockedEventRecorder.
		On("Event", mock.Anything, "Normal", "SecretBootstrap", "Created image pull secret")

	// check for event message after RBAC related resources were bootstrapped
	mockedEventRecorder.
		On("Event", mock.Anything, "Normal", "RbacBootstrap", "Created RBAC resources")

	// setup the MockWriter (client.Create(), client.Update() calls)

	databaseSecretName := runtimeNameTestInitial + "-systemdb"
	databaseSecretMatcher := func(secret *core.Secret) bool {
		ownerRefOk := checkOwnerRefOk(secret)
		secretUserOk := string(secret.Data["username"]) == dbDesc.DatabaseUser
		secretPassOk := string(secret.Data["password"]) == dbDesc.DatabasePassword
		secretNameOk := secret.Name == databaseSecretName
		return ownerRefOk && secretNameOk && secretUserOk && secretPassOk
	}

	imagePullSecretName := runtimeNameTestInitial + "-bt-artifactory"
	imagePullSecretMatcher := func(secret *core.Secret) bool {
		ownerRefOk := checkOwnerRefOk(secret)
		secretDockJson := secret.Data[".dockerconfigjson"] != nil
		secretNameOk := secret.Name == imagePullSecretName
		return ownerRefOk && secretNameOk && secretDockJson
	}

	serviceAccountName := runtimeNameTestInitial
	serviceAccountMatcher := func(serviceAccount *core.ServiceAccount) bool {
		ownerRefOk := checkOwnerRefOk(serviceAccount)
		serviceAccountNameOk := serviceAccount.Name == serviceAccountName
		return ownerRefOk && serviceAccountNameOk
	}

	roleName := runtimeNameTestInitial
	roleMatcher := func(role *rbacv1.Role) bool {
		ownerRefOk := checkOwnerRefOk(role)
		roleNameOk := role.Name == roleName
		return ownerRefOk && roleNameOk
	}

	roleBindingName := runtimeNameTestInitial
	roleBindingMatcher := func(roleBinding *rbacv1.RoleBinding) bool {
		ownerRefOk := checkOwnerRefOk(roleBinding)
		roleBindingNameOk := roleBinding.Name == roleBindingName
		roleBindingRoleRefOk := roleBinding.RoleRef.Name == roleName
		roleBindingSubjectOk := roleBinding.Subjects[0].Name == roleName
		return ownerRefOk && roleBindingRoleRefOk && roleBindingNameOk && roleBindingSubjectOk
	}

	// check that a valid database secret is created
	mockWriter.On("Create", context.Background(), mock.MatchedBy(databaseSecretMatcher)).Return(nil)

	// check that a valid docker pull secret is created
	mockWriter.On("Create", context.Background(), mock.MatchedBy(imagePullSecretMatcher)).Return(nil)

	// check that a valid service account is created
	mockWriter.On("Create", context.Background(), mock.MatchedBy(serviceAccountMatcher)).Return(nil)

	// check that a valid role is created
	mockWriter.On("Create", context.Background(), mock.MatchedBy(roleMatcher)).Return(nil)

	// check that a valid role binding is created
	mockWriter.On("Create", context.Background(), mock.MatchedBy(roleBindingMatcher)).Return(nil)

	//////////////////////////
	//
	// check the components
	//
	//////////////////////////

	//
	// create tribefire-master deployment, service and ingress
	//
	expectedMasterLabels := map[string]string{
		"initiative": runtimeNameTestInitial,
		"workspace":  runtimeNamespace,
		"app":        "tribefire-master",
		"runtime":    runtimeNameTestInitial,
		"stage":      "default",
	}

	setupMatchers("master", expectedMasterLabels, "/services", mockWriter, mockedEventRecorder)

	//
	// create control-center deployment, service and ingress
	//
	expectedCcLabels := map[string]string{
		"initiative": runtimeNameTestInitial,
		"workspace":  runtimeNamespace,
		"app":        "tribefire-control-center",
		"runtime":    runtimeNameTestInitial,
		"stage":      "default",
	}

	setupMatchers("control-center", expectedCcLabels, "/control-center", mockWriter, mockedEventRecorder)

	//
	// create explorer deployment, service and ingress
	//
	expectedExplorerLabels := map[string]string{
		"initiative": runtimeNameTestInitial,
		"workspace":  runtimeNamespace,
		"app":        "tribefire-explorer",
		"runtime":    runtimeNameTestInitial,
		"stage":      "default",
	}

	setupMatchers("explorer", expectedExplorerLabels, "/explorer", mockWriter, mockedEventRecorder)

	//
	// create explorer deployment, service and ingress
	//
	expectedModelerLabels := map[string]string{
		"initiative": runtimeNameTestInitial,
		"workspace":  runtimeNamespace,
		"app":        "tribefire-modeler",
		"runtime":    runtimeNameTestInitial,
		"stage":      "default",
	}

	setupMatchers("modeler", expectedModelerLabels, "/modeler", mockWriter, mockedEventRecorder)

	//
	// setup status update and reader mocks
	//
	mockStatusWriter.On("Update", context.Background(), mock.Anything).Return(nil)

	mockReader.
		On("Get",
			context.Background(),
			mock.MatchedBy(buildObjectKeyNameMatcher(runtimeNameTestInitial)), mock.Anything).
		Return(nil)

	// final reconcile event message
	// check for event message after tribefire-master was created
	mockedEventRecorder.
		On("Event", mock.Anything, "Normal", "TribefireRuntimeReconciled", "TribefireRuntime reconciled")

	mockEtcdChecker.On("Check", mock.Anything).Return(nil)

	//
	// let's go
	//
	r := createReconcilerWithMockedClients(mockReader, mockWriter, mockStatusClient, &dbMgrMock, mockedEventRecorder)
	r.EtcdChecker = mockEtcdChecker

	request := reconcile.Request{NamespacedName: objectKey(runtimeNameTestInitial)}

	result, err := r.Reconcile(context.TODO(), request)
	assert.Nil(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	mockReader.AssertExpectations(t)
}

// test the case where the runtime has the DeletionTimestamp set but the finalizer is still there.
// finalizer should be removed and an event should be submitted
func TestReconcileRuntimeFinalizeLocalDatabase(t *testing.T) {
	mockReader, _, key := setupMockReader(runtimeNameTestDeleteLocal)
	mockWriter := new(MockWriter)

	finalizerExistsMatcher := mock.MatchedBy(func(tf *tribefirev1.TribefireRuntime) bool {
		return len(tf.Finalizers) > 0 && len(tf.Finalizers) < 2
	})

	finalizerGoneMatcher := mock.MatchedBy(func(tf *tribefirev1.TribefireRuntime) bool {
		return len(tf.Finalizers) == 0
	})

	mockReader.On("Get", context.Background(), key, mock.Anything).Return(nil)

	mockWriter.
		On("Update", mock.Anything, finalizerGoneMatcher).
		Return(nil)

	mockedEventRecorder := &MockEventRecorder{}

	// event for start of finalization
	expectedMessage := fmt.Sprintf("Finalizing TribefireRuntime")
	mockedEventRecorder.On("Event", finalizerExistsMatcher, "Normal", "TribefireRuntimeFinalizing", expectedMessage).Once()

	// event for finished finalization
	expectedMessage = fmt.Sprintf("TribefireRuntime finalized")
	mockedEventRecorder.On("Event", finalizerGoneMatcher, "Normal", "TribefireRuntimeFinalized", expectedMessage).Once()

	r := createReconcilerWithMockedClients(mockReader, mockWriter, nil, nil, mockedEventRecorder)
	request := reconcile.Request{NamespacedName: objectKey(runtimeNameTestDeleteLocal)}

	result, err := r.Reconcile(context.TODO(), request)
	assert.Nil(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	mockReader.AssertExpectations(t)
}

// test the case where the runtime has the DeletionTimestamp set but the finalizer is still there
// fetch latest fails during that
func TestReconcileRuntimeFinalizeFetchLatestFails(t *testing.T) {
	mockReader, _, key := setupMockReader(runtimeNameTestDeleteLocal)
	mockWriter := new(MockWriter)

	// first client.Get
	mockReader.
		On("Get", context.Background(), key, mock.Anything).
		Return(nil).
		Once()

	// second client.Get in fetchLatest(), will fail
	mockReader.
		On("Get", context.Background(), key, mock.Anything).
		Return(k8serr.NewInternalError(errors.New("Something really bad happened"))).
		Once()

	// finalizer should still exist at this point
	finalizerExistsMatcher := mock.MatchedBy(func(tf *tribefirev1.TribefireRuntime) bool {
		return len(tf.Finalizers) > 0 && len(tf.Finalizers) < 2
	})

	// event for start of finalization
	mockedEventRecorder := &MockEventRecorder{}
	expectedMessage := fmt.Sprintf("Finalizing TribefireRuntime")
	mockedEventRecorder.On("Event", finalizerExistsMatcher, "Normal", "TribefireRuntimeFinalizing", expectedMessage).Once()

	r := createReconcilerWithMockedClients(mockReader, mockWriter, nil, nil, mockedEventRecorder)
	request := reconcile.Request{NamespacedName: objectKey(runtimeNameTestDeleteLocal)}

	// Reconcile should return an error since fetchLatest() returned an error
	result, err := r.Reconcile(context.TODO(), request)
	assert.NotNil(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	mockReader.AssertExpectations(t)
}

// test the case where the runtime has the DeletionTimestamp set but the finalizer is still there
// fetch latest fails during that
func TestReconcileRuntimeFinalizeFinalUpdateFails(t *testing.T) {
	mockReader, _, key := setupMockReader(runtimeNameTestDeleteLocal)
	mockWriter := new(MockWriter)

	mockReader.On("Get", context.Background(), key, mock.Anything).Return(nil)

	mockWriter.
		On("Update", mock.Anything, mock.Anything).
		Return(k8serr.NewInternalError(errors.New("Something really bad happened")))

	// finalizer should still exist at this point
	finalizerExistsMatcher := mock.MatchedBy(func(tf *tribefirev1.TribefireRuntime) bool {
		return len(tf.Finalizers) > 0 && len(tf.Finalizers) < 2
	})

	// event for start of finalization
	mockedEventRecorder := &MockEventRecorder{}
	expectedMessage := fmt.Sprintf("Finalizing TribefireRuntime")
	mockedEventRecorder.On("Event", finalizerExistsMatcher, "Normal", "TribefireRuntimeFinalizing", expectedMessage).Once()

	r := createReconcilerWithMockedClients(mockReader, mockWriter, nil, nil, mockedEventRecorder)
	request := reconcile.Request{NamespacedName: objectKey(runtimeNameTestDeleteLocal)}

	// Reconcile should return an error since fetchLatest() returned an error
	result, err := r.Reconcile(context.TODO(), request)
	assert.NotNil(t, err)
	assert.Equal(t, reconcile.Result{}, result)
	mockReader.AssertExpectations(t)
}

// helper functions
func createReconcilerWithMockedClients(
	reader client.Reader,
	writer client.Writer,
	status client.StatusClient,
	dbMgr tribefire.TribefireDatabaseMgr,
	recorder record.EventRecorder) *TribefireRuntimeReconciler {

	mockedClient := MockedClient{
		reader, writer, status,
	}

	return &TribefireRuntimeReconciler{
		Client:        mockedClient,
		EventRecorder: recorder,
		DbMgr:         dbMgr,
	}
}

type MockedClient struct {
	client.Reader
	client.Writer
	client.StatusClient
}

func (h MockedClient) Scheme() *runtime.Scheme {
	return nil
}

func (h MockedClient) RESTMapper() meta.RESTMapper {
	return nil
}

func (h MockedClient) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	return schema.GroupVersionKind{}, nil
}

func (h MockedClient) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	return true, nil
}

func (h MockedClient) SubResource(subResource string) client.SubResourceClient {
	return nil
}

func buildTribefireRuntime(name string, namespace string) *tribefirev1.TribefireRuntime {
	tf := &tribefirev1.TribefireRuntime{}
	tf.Name = name
	tf.Namespace = namespace
	tf.Generation = 0

	masterComponent := tribefirev1.TribefireComponent{Name: "tribefire-master", Type: tribefirev1.Services}
	controlCenterComponent := tribefirev1.TribefireComponent{Name: "tribefire-control-center", Type: tribefirev1.ControlCenter}
	explorerComponent := tribefirev1.TribefireComponent{Name: "tribefire-explorer", Type: tribefirev1.Explorer}
	modelerComponent := tribefirev1.TribefireComponent{Name: "tribefire-modeler", Type: tribefirev1.Modeler}
	tf.Spec.Components = append(tf.Spec.Components, masterComponent)
	tf.Spec.Components = append(tf.Spec.Components, controlCenterComponent)
	tf.Spec.Components = append(tf.Spec.Components, modelerComponent)
	tf.Spec.Components = append(tf.Spec.Components, explorerComponent)

	_, _ = tribefirev1.SetDefaults(tf)

	return tf
}

func objectKey(name string) client.ObjectKey {
	return client.ObjectKey{
		Name:      name,
		Namespace: runtimeNamespace,
	}
}

func buildObjectKeyNameMatcher(runtimeName string) func(client.ObjectKey) bool {
	return func(key client.ObjectKey) bool {
		if key.Name == runtimeName {
			return true
		}

		for _, suffix := range validDeploymentNameSuffixes {
			componentName := runtimeName + "-" + suffix
			if componentName == key.Name {
				return true
			}
		}

		return false
	}
}

func checkOwnerRefOk(obj metav1.Object) bool {
	if len(obj.GetOwnerReferences()) > 0 {
		ownerKindOk := obj.GetOwnerReferences()[0].Kind == "TribefireRuntime"
		ownerNameOk := obj.GetOwnerReferences()[0].Name == runtimeNameTestInitial
		return ownerKindOk && ownerNameOk
	}

	return false
}

func setupMatchers(component string, labels map[string]string, path string, mockWriter *MockWriter, mockedEventRecorder *MockEventRecorder) {

	ingressHost := runtimeNameTestInitial + "-" + runtimeNamespace + ".tribefire.local"

	deployName := runtimeNameTestInitial + "-tribefire-" + component
	deployMatcher := createDeploymentMatcher(deployName, labels)

	serviceName := deployName
	serviceMatcher := createServiceMatcher(serviceName, labels)

	ingressName := deployName
	ingressMatcher := createIngressMatcher(ingressName, ingressHost, path, labels)

	// check for event message after tribefire-master was created
	mockedEventRecorder.
		On("Event", mock.Anything, "Normal", "ComponentDeployment", "Created tribefire-"+component)

	// check that a master deployment is created
	mockWriter.On("Create", context.Background(),
		mock.MatchedBy(deployMatcher)).Return(nil)

	// check that a master service is created
	mockWriter.On("Create", context.Background(),
		mock.MatchedBy(serviceMatcher)).Return(nil)

	// check that a master ingress is created
	mockWriter.On("Create", context.Background(),
		mock.MatchedBy(ingressMatcher)).Return(nil)
}

func createDeploymentMatcher(name string, labels map[string]string) func(obj *apps.Deployment) bool {
	return func(obj *apps.Deployment) bool {
		return checkObjectMetadata(obj.GetObjectMeta(), name, labels)
	}
}

func createServiceMatcher(name string, labels map[string]string) func(obj *core.Service) bool {
	return func(obj *core.Service) bool {
		return checkObjectMetadata(obj.GetObjectMeta(), name, labels)
	}
}

func createIngressMatcher(name string, host string, path string, labels map[string]string) func(obj *net.Ingress) bool {
	port := net.ServiceBackendPort{
		Number: 8080,
	}

	return func(obj *net.Ingress) bool {
		metaOk := checkObjectMetadata(obj.GetObjectMeta(), name, labels)
		ingressHostOk := obj.Spec.Rules[0].Host == host
		ingressPathOk := obj.Spec.Rules[0].HTTP.Paths[0].Path == path
		serviceNameOk := obj.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name == name
		servicePortOk := obj.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port == port
		return metaOk && serviceNameOk && servicePortOk && ingressHostOk && ingressPathOk
	}
}

func checkObjectMetadata(obj metav1.Object, name string, labels map[string]string) bool {
	ownerRefOk := checkOwnerRefOk(obj)
	nameOk := obj.GetName() == name
	namespaceOk := obj.GetNamespace() == runtimeNamespace
	labelsOk := cmp.Diff(obj.GetLabels(), labels)
	return ownerRefOk && nameOk && namespaceOk && labelsOk == ""
}

// test setup
func init() {
	CacheSettleRetries = 0
}
