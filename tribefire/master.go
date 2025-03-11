package tribefire

import (
	"github.com/thoas/go-funk"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	net "k8s.io/api/networking/v1"
	tribefirev1 "tribefire-operator/api/v1"
	"tribefire-operator/common"
)

const (
	HttpPort                    = 8080
	JpdaPort                    = 8000
	HealthCheckPort             = 8080
	MaxUnavailable              = 1
	MaxSurge                    = 1
	HealthCheckPath             = "/rpc"
	IngressPathMaster           = "/services"
	MasterAppName               = "tribefire-master"
	PostgresDefaultCheckerImage = "docker.artifactory.braintribe.com/tribefire-cloud/postgres-checker:0.0.4" //previous version is the default
	SystemDbCredentialsFile     = "/secrets/cloudsql/system.json"
)

var postgresCheckerImage = getEnvOrDefault("TRIBEFIRE_POSTGRESQL_CHECKER_IMAGE", PostgresDefaultCheckerImage)

var defaultMode int32 = 420

func NewTribefireMasterService(tf *tribefirev1.TribefireRuntime) *core.Service {
	ports := []core.ServicePort{
		{Name: "http", Protocol: "TCP", Port: HttpPort},
	}

	return newService(tf, MasterAppName, ports)
}

func NewTribefireMasterIngress(tf *tribefirev1.TribefireRuntime, c *tribefirev1.TribefireComponent) *net.Ingress {
	ingress := NewIngress(tf, c, MasterAppName, IngressPathMaster)
	addOwnerRefToObject(ingress, asOwner(tf))
	dumpResourceToStdout(ingress)
	return ingress
}

func NewTribefireMasterDeployment(tf *tribefirev1.TribefireRuntime, component *tribefirev1.TribefireComponent) *apps.Deployment {
	secretName := buildDefaultCloudSqlServiceAccountSecretName(tf)

	additionalLabels := make(map[string]string)
	additionalLabels["jsonlog"] = component.LogJson

	healthCheckPath := common.CustomHealthCheckPath()
	if healthCheckPath == "" {
		healthCheckPath = HealthCheckPath
	}

	podSpec := createPod(tf, component, additionalLabels, MasterAppName, healthCheckPath)

	if !tf.IsLocalDatabase() && common.CloudSqlProxyEnabled() {
		cloudsqlProxyContainer := buildCloudSqlProxyContainer("cloud-sql-proxy",
			buildDefaultInstanceString(), SystemDbCredentialsFile)

		containers := append(podSpec.Spec.Containers, cloudsqlProxyContainer)
		podSpec.Spec.Containers = containers

		podSpec.Spec.Volumes = append(podSpec.Spec.Volumes,
			buildCloudSqlProxyVolumes("cloudsql-credentials", secretName))

	}

	if tf.HasAdditionalDatabases() && common.CloudSqlProxyEnabled() {
		var existingProxies []string
		for _, dbSpec := range tf.Spec.AdditionalDatabases {
			proxyString := dbSpec.InstanceDescriptor + "/" + dbSpec.DatabaseName
			if funk.ContainsString(existingProxies, proxyString) {
				continue // todo handle duplicate definitions
			}

			cloudsqlProxyContainer := buildCloudSqlProxyContainer(dbSpec.Name+"-cloud-sql-proxy",
				dbSpec.InstanceDescriptor, "/secrets/cloudsql/"+dbSpec.ServiceAccountSecretKey)

			podSpec.Spec.Containers = append(podSpec.Spec.Containers, cloudsqlProxyContainer)

			podSpec.Spec.Volumes = append(podSpec.Spec.Volumes,
				buildCloudSqlProxyVolumes("cloudsql-credentials", dbSpec.ServiceAccountSecretRef.Name))

		}
	}

	if tf.IsLocalDatabase() && common.PostgresCheckerEnabled() {
		postgresCheckerContainer := core.Container{
			Name:            "postgres-checker",
			Image:           postgresCheckerImage,
			ImagePullPolicy: "Always",
			Env: []core.EnvVar{
				{Name: "PGHOST", Value: DefaultResourceName(tf, PostgresAppName)},
			},
		}

		initContainers := append(podSpec.Spec.InitContainers, postgresCheckerContainer)
		podSpec.Spec.InitContainers = initContainers
	}

	deployment := newDeployment(tf, MasterAppName, &podSpec, component.Replicas)
	addOwnerRefToObject(deployment, asOwner(tf))
	dumpResourceToStdout(deployment)
	return deployment
}

func buildCloudSqlProxyContainer(name, instanceString, credentialsFile string) core.Container {
	return core.Container{
		Name:  name,
		Image: "gcr.io/cloudsql-docker/gce-proxy:latest",
		Command: []string{
			"/cloud_sql_proxy",
			"--dir=/cloudsql",
			"-instances=" + instanceString,
			"-credential_file=" + credentialsFile,
		},
		Ports: []core.ContainerPort{
			{Name: "http", ContainerPort: 8080, Protocol: "TCP"},
		},
		VolumeMounts: []core.VolumeMount{
			{
				Name:      "cloudsql-credentials",
				MountPath: "/secrets/cloudsql",
			},
		},
	}
}

func buildCloudSqlProxyVolumes(name, secretName string) core.Volume {
	return core.Volume{
		Name: name,
		VolumeSource: core.VolumeSource{

			Secret: &core.SecretVolumeSource{
				DefaultMode: &defaultMode,
				SecretName:  secretName,
			},
		},
	}
}
