package tribefire

import (
	"context"

	core "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	tribefirev1 "tribefire-operator/api/v1"
	"tribefire-operator/providers"

	. "tribefire-operator/common"
)

const (
	PostgresAppName      = "postgres"
	PostgresPassword     = "tribefire"
	PostgresUser         = "tribefire"
	PostgresDatabase     = "postgres"
	PostgresPort         = 5432
	PostgresDefaultImage = "bitnami/postgresql:17"
)

var projectId = os.Getenv("TRIBEFIRE_GCP_DATABASES_PROJECT_ID")
var instanceId = os.Getenv("TRIBEFIRE_GCP_DATABASES_INSTANCE_ID")
var postgresImage = getEnvOrDefault("TRIBEFIRE_POSTGRESQL_IMAGE", PostgresDefaultImage)

type TribefireDatabaseMgr interface {
	CreateDatabase(tf *tribefirev1.TribefireRuntime) (*providers.DatabaseDescriptor, error)
	DeleteDatabase(tf *tribefirev1.TribefireRuntime) error
}

type DefaultTribefireDatabaseMgr struct {
	client client.Client
}

func NewTribefireDatabaseMgr(client client.Client) TribefireDatabaseMgr {
	return &DefaultTribefireDatabaseMgr{client: client}
}

// create a database. Either via configured provider or via local deployment (e.g. local PostgreSQL)
func (d *DefaultTribefireDatabaseMgr) CreateDatabase(tf *tribefirev1.TribefireRuntime) (*providers.DatabaseDescriptor, error) {
	switch tf.Spec.DatabaseType {
	case tribefirev1.CloudSqlDatabase:
		return d.createDatabaseViaProvider(tf)
	case tribefirev1.LocalPostgresql:
		return d.createLocalDatabase(tf)
	default:
		return nil, tribefirev1.UnsupportedDatabaseError
	}
}

// delete the database associated with this initiative. Only relevant if database was created via Provider
func (d *DefaultTribefireDatabaseMgr) DeleteDatabase(tf *tribefirev1.TribefireRuntime) error {

	if tf.IsLocalDatabase() {
		return nil
	}
	//
	//databaseProvider := providers.NewCloudSqlProvider(projectId, instanceId, false)
	//databaseDesc := createDescriptor(tf)
	//
	//err := databaseProvider.DeleteDatabase(databaseDesc)
	//if err != nil {
	//	L().Errorf("Database deletion failed: %v", err)
	//	return err
	//}
	//
	//// check if database was really removed, and wait a little bit for the deletion to settle by retrying
	//err = checkGone(databaseDesc, "Database", providers.DatabaseDoesNotExist, databaseProvider.RetrieveDatabase)
	//if err != nil {
	//	return errors.New(fmt.Sprintf("Database '%s' is still here after %d seconds",
	//		databaseDesc.DatabaseName, providers.RetryDelayDeleteSeconds*providers.RetriesDelete))
	//}
	//
	//err = databaseProvider.DeleteUser(databaseDesc)
	//if err != nil {
	//	L().Errorf("User deletion failed: %v", err)
	//	return err
	//}
	//
	//// check if database was really removed, and wait a little bit for the deletion to settle by retrying
	//err = checkGone(databaseDesc, "User", providers.UserDoesNotExist, databaseProvider.RetrieveUser)
	//if err != nil {
	//	return errors.New(fmt.Sprintf("User '%s' is still here after %d seconds",
	//		databaseDesc.DatabaseUser, providers.RetryDelayDeleteSeconds*providers.RetriesDelete))
	//}
	//
	//return err

	return nil
}

//
// helper that validates given resource (user or database) is truly gone
//
//func checkGone(
//	databaseDesc *providers.DatabaseDescriptor,
//	resource string,
//	expectedErr error,
//	checkFunc func(*providers.DatabaseDescriptor) (*providers.DatabaseDescriptor, error)) error {
//
//	retryDelay := providers.RetryDelayDeleteSeconds * time.Second
//	for retries := providers.RetriesDelete; retries >= 0; retries-- {
//		if _, err := checkFunc(databaseDesc); err != nil {
//			if err == expectedErr {
//				L().Debugf("%s '%s' gone. All good.", resource, databaseDesc.String())
//				return nil
//			}
//		} else {
//			L().Debugf("%s '%s' still shows up. Retries left: %d", resource, databaseDesc.String(), retries)
//			time.Sleep(retryDelay)
//		}
//	}
//
//	return errors.New(fmt.Sprintf("%s '%s' still shows up after %d seconds",
//		resource, databaseDesc.String(), providers.RetryDelayDeleteSeconds*providers.RetriesDelete))
//}

// deploys a local PostgreSQL instance
func (d *DefaultTribefireDatabaseMgr) createLocalDatabase(tf *tribefirev1.TribefireRuntime) (*providers.DatabaseDescriptor, error) {
	additionalLabels := make(map[string]string)
	podSpec := core.PodTemplateSpec{
		ObjectMeta: meta.ObjectMeta{
			Name:      DefaultResourceName(tf, PostgresAppName),
			Namespace: tf.Namespace,
			Labels:    buildLabelSet(tf, PostgresAppName, additionalLabels),
		},
		Spec: core.PodSpec{
			ServiceAccountName: buildDefaultServiceAccountName(tf),
			Containers: []core.Container{
				{
					Name:            PostgresAppName,
					Image:           postgresImage,
					ImagePullPolicy: getPullPolicy(),
					Ports: []core.ContainerPort{
						{
							ContainerPort: PostgresPort,
							Protocol:      "TCP",
						},
					},
					Env: []core.EnvVar{
						{
							Name:  "POSTGRES_PASSWORD",
							Value: PostgresPassword,
						}, {
							Name:  "POSTGRES_USER",
							Value: PostgresUser,
						},
					},
				},
			},
		},
	}

	deployment := newDeployment(tf, PostgresAppName, &podSpec, 1)
	addOwnerRefToObject(deployment, asOwner(tf))
	dumpResourceToStdout(deployment)
	err := d.client.Create(context.TODO(), deployment)
	if err != nil && !k8serr.IsAlreadyExists(err) {
		L().Errorf("Cannot create postgres deployment: %v", err)
		return nil, err
	}

	service := newService(tf, PostgresAppName, []core.ServicePort{
		{
			Name: PostgresAppName, Protocol: "TCP", Port: PostgresPort, TargetPort: intstr.FromInt(PostgresPort),
		},
	})

	dumpResourceToStdout(service)
	err = d.client.Create(context.TODO(), service)
	if err != nil && !k8serr.IsAlreadyExists(err) {
		L().Errorf("Cannot create postgres deployment: %v", err)
		return nil, err
	}

	return &providers.DatabaseDescriptor{
		DatabaseName:     PostgresDatabase,
		DatabaseUser:     PostgresUser,
		DatabasePassword: PostgresPassword,
	}, err
}

// creates a Cloud database, currently only Google CloudSQL is supported
func (d DefaultTribefireDatabaseMgr) createDatabaseViaProvider(tf *tribefirev1.TribefireRuntime) (*providers.DatabaseDescriptor, error) {
	//databaseProvider := providers.NewCloudSqlProvider(projectId, instanceId, false)
	//databaseDesc := createDescriptor(tf)
	//
	//_, err := databaseProvider.CreateDatabase(databaseDesc)
	//if err != nil && err == providers.DatabaseAlreadyExists {
	//	L().Debugf("Database '%s' already exists, noop", databaseDesc.DatabaseName)
	//	return databaseDesc, err
	//}
	//
	//if err != nil {
	//	L().Errorf("Unexpected error during database create: %v", err)
	//	return databaseDesc, err
	//}
	//
	//L().Infof("Created database '%s'", databaseDesc.DatabaseName)
	//
	//dbDesc, err := databaseProvider.RetrieveUser(databaseDesc)
	//if err != nil {
	//	if err == providers.UserDoesNotExist {
	//		L().Debugf("User '%s' does not exist, creating", databaseDesc.DatabaseUser)
	//		return databaseProvider.CreateUser(databaseDesc)
	//	}
	//
	//	L().Errorf("Cannot fetch user information from DB: %v", err)
	//	return nil, err
	//}
	//
	//L().Debugf("User '%s' already exists, noop", databaseDesc.DatabaseUser)
	//return dbDesc, err
	return nil, nil
}

func createDescriptor(tf *tribefirev1.TribefireRuntime) *providers.DatabaseDescriptor {
	databaseName := createDatabaseName(tf)
	dbUser := buildRuntimeFqName(tf)

	return &providers.DatabaseDescriptor{
		ProjectId:    projectId,
		InstanceId:   instanceId,
		DatabaseName: databaseName,
		DatabaseUser: dbUser,
	}
}

func createDatabaseName(tf *tribefirev1.TribefireRuntime) string {
	return buildRuntimeFqName(tf)
}
