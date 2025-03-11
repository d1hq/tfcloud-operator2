package tribefire

import (
	"encoding/base64"
	"go.etcd.io/etcd/client/pkg/v3/fileutil"
	"k8s.io/api/core/v1"
	core "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/util/intstr"
	"net/url"
	"os"
	"strings"
	tribefirev1 "tribefire-operator/api/v1"
	"tribefire-operator/common"
)

// template for Docker image pull secrets
const imagePullSecretYaml = `{
  "auths": {
    "@@DOCKERHOST@@": {
      "username": "@@USER@@",
      "password": "@@PASSWORD@@",
      "email": "some@mail.com",
      "auth": "@@AUTH@@"
    }
  }
}
`

const DefaultProbeTimeout = 10

// create the connection string for the CloudSQL instance
func buildDefaultInstanceString() string {
	projectId := os.Getenv("TRIBEFIRE_GCP_DATABASES_PROJECT_ID")
	instanceId := os.Getenv("TRIBEFIRE_GCP_DATABASES_INSTANCE_ID")
	region := os.Getenv("TRIBEFIRE_GCP_DATABASES_REGION")
	return projectId + ":" + region + ":" + instanceId + "=tcp:5432"
}

// creates the label set that is used to identify this Tribefire instance. Also used as a selector
// in the service definition
func buildLabelSet(tf *tribefirev1.TribefireRuntime, app string, additionalLabels map[string]string) map[string]string {
	stage := getLabel(tf, "stage")
	labels := map[string]string{
		"app":        app,
		"initiative": tf.Name,
		"workspace":  tf.Namespace,
		"runtime":    tf.Name,
	}

	if stage != "" {
		labels["stage"] = stage
		initiative, _ := SplitRuntimeName(tf)
		labels["initiative"] = initiative
	}

	if len(additionalLabels) > 0 {
		for key, value := range additionalLabels {
			labels[key] = value
		}
	}

	return labels

}
func getLabel(tf *tribefirev1.TribefireRuntime, labelName string) string {
	if len(tf.Labels) > 0 && tf.Labels[labelName] != "" {
		return tf.Labels[labelName]
	}

	return ""
}

func dumpResourceToStdout(resource runtime.Object) {
	dumpResources := os.Getenv("TRIBEFIRE_OPERATOR_DUMP_RESOURCES_STDOUT")
	if dumpResources == "true" {
		println("---")
		e := json.NewYAMLSerializer(json.DefaultMetaFactory, nil, nil)
		e.Encode(resource, os.Stdout)
	}
}

func buildDockerConfigJson() []byte {
	pullSecretsUser := os.Getenv("TRIBEFIRE_PULL_SECRETS_USER")
	pullSecretsPassword := os.Getenv("TRIBEFIRE_PULL_SECRETS_PASSWORD")
	pullSecretsAuth := base64.StdEncoding.EncodeToString([]byte(pullSecretsUser + ":" + pullSecretsPassword))

	yaml := strings.Replace(imagePullSecretYaml, "@@USER@@", pullSecretsUser, 1)
	yaml = strings.Replace(yaml, "@@PASSWORD@@", pullSecretsPassword, 1)
	yaml = strings.Replace(yaml, "@@AUTH@@", pullSecretsAuth, 1)
	yaml = strings.Replace(yaml, "@@DOCKERHOST@@", getDockerHostUrl(), 1)
	return []byte(yaml)
}

// addOwnerRefToObject appends the desired OwnerReference to the object
func addOwnerRefToObject(o meta.Object, r meta.OwnerReference) {
	o.SetOwnerReferences(append(o.GetOwnerReferences(), r))
}

// asOwner returns an owner reference set as the vault cluster CR
func asOwner(tf *tribefirev1.TribefireRuntime) meta.OwnerReference {
	trueVar := true
	return meta.OwnerReference{
		APIVersion:         tribefirev1.GroupVersion.String(),
		Kind:               tribefirev1.Kind,
		Name:               tf.Name,
		UID:                tf.UID,
		Controller:         &trueVar,
		BlockOwnerDeletion: &trueVar,
	}
}

func buildRuntimeFqName(tf *tribefirev1.TribefireRuntime) string {
	return tf.Name + "-" + tf.Namespace
}

func buildBaseDomain(tf *tribefirev1.TribefireRuntime) string {
	if common.DisableNameParsingForIngress() {
		return tf.Name + "-" + tf.Namespace + "." + tf.Spec.Domain
	}

	if common.EnableShortDomainNames() {
		return tf.Name + "." + tf.Spec.Domain
	}

	name, stage := SplitRuntimeName(tf)

	if stage != "" {
		return stage + "-" + name + "-" + tf.Namespace + "." + tf.Spec.Domain
	}

	return tf.Name + "-" + tf.Namespace + "." + tf.Spec.Domain
}

// the convention is to add the `stage` as a prefix to the name of the TribefireRuntime.
// so we take everything before the last `-` as the actual TribefireRuntime name, and everything
// after the last `-` is treated as the stage name.
// if there is a `stage` label, we just remove that from the name of the runtime
func SplitRuntimeName(tf *tribefirev1.TribefireRuntime) (string, string) {
	var stage string
	name := tf.Name
	if tf.ObjectMeta.GetLabels() != nil {
		stage = tf.ObjectMeta.GetLabels()["stage"]
	}

	stageSuffix := "-" + stage
	stagePrefix := stage + "-"
	hasStageSuffix := strings.HasSuffix(name, stageSuffix)
	hasStagePrefix := strings.HasPrefix(name, stagePrefix)
	if stage != "default" && hasStageSuffix {
		name = strings.TrimSuffix(name, stageSuffix)
		return name, stage
	}

	if stage != "default" && hasStagePrefix {
		name = strings.TrimPrefix(name, stagePrefix)
		return name, stage
	}

	// todo this might make sense, or it won't
	//lastHyphen := strings.LastIndex(name, "-")
	//if lastHyphen != -1 {
	//	actualName := name[0:lastHyphen]
	//	stage = name[lastHyphen+1:]
	//	return actualName, stage
	//}

	return name, ""
}

func DefaultResourceName(tf *tribefirev1.TribefireRuntime, app string) string {
	return tf.Name + "-" + app
}

func extractHostNamePort(publicUrl string) string {
	parsedUrl, _ := url.Parse(publicUrl) // validation already happened through OpenAPI validation
	return parsedUrl.Host
}

func getPullPolicy() v1.PullPolicy {
	pullPolicy := os.Getenv("TRIBEFIRE_IMAGE_PULL_POLICY")
	switch pullPolicy {
	case "Always":
		return v1.PullAlways
	case "Never":
		return v1.PullNever
	case "IfNotPresent":
		return v1.PullIfNotPresent
	default:
		return v1.PullAlways
	}
}

func newReadinessProbe(path string, port int, delay int32) *core.Probe {
	return newProbe(path, port, delay, 3, 3)
}

func newLivenessProbe(path string, port int, delay int32) *core.Probe {
	return newProbe(path, port, delay, 3, 10)
}

func newProbe(path string, port int, delay int32, failureThreshold int32, interval int32) *core.Probe {
	return &core.Probe{
		InitialDelaySeconds: delay,
		FailureThreshold:    failureThreshold,
		PeriodSeconds:       interval,
		TimeoutSeconds:      DefaultProbeTimeout,
		ProbeHandler: core.ProbeHandler{
			HTTPGet: &core.HTTPGetAction{
				Path: path,
				Port: intstr.FromInt(port),
			},
		},
	}
}

func IsCloudSqlEnabled() bool {
	return fileutil.Exist("/cloudsql/system.json")
}

// Get the URL of docker host, default is dockerregistry.example.com
func getDockerHostUrl() string {
	dockerHost := os.Getenv("OPERATOR_DOCKER_HOST")
	if dockerHost == "" {
		return "dockerregistry.example.com"
	}
	return dockerHost
}

func getEnvOrDefault(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}
