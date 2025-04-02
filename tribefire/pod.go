package tribefire

import (
	core "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	tribefirev1 "tribefire-operator/api/v1"
	"tribefire-operator/common"
)

const (
	LivenessProbeInitialDelay  = 300
	ReadinessProbeInitialDelay = 7
	WritableVolumeName         = "writable"
	WritableVolumeMountPath    = "/writable"
	WritableRunVolumeName      = "writable-run"
	WritableRunVolumeMountPath = "/writable/run"
)

var (
	TribefireMasterPriorityClassName    = "tribefire-master"
	TribefireCartridgePriorityClassName = "tribefire-cartridge"
	TribefireComponentPriorityClassName = "tribefire-component"
)

func createPod(tf *tribefirev1.TribefireRuntime, component *tribefirev1.TribefireComponent,
	additionalLabels map[string]string, appName string, healthCheckPath string) core.PodTemplateSpec {

	healthCheckPath = getHealthCheckPath(component, healthCheckPath)

	readinessCheckPath := healthCheckPath
	readinessCheckPath = getReadinessCheckPath(component, readinessCheckPath)

	pod := core.PodTemplateSpec{
		ObjectMeta: meta.ObjectMeta{
			Name:      DefaultResourceName(tf, appName),
			Namespace: tf.Namespace,
			Labels:    buildLabelSet(tf, appName, additionalLabels),
		},
		Spec: core.PodSpec{
			ImagePullSecrets: []core.LocalObjectReference{
				{
					Name: buildDefaultImagePullSecretName(tf),
				},
			},
			ServiceAccountName: buildDefaultServiceAccountName(tf),
			Containers: []core.Container{
				{
					Name:            appName,
					Image:           component.Image + ":" + component.ImageTag,
					Env:             *buildEnvVars(tf, component),
					ReadinessProbe:  newReadinessProbe(readinessCheckPath, HealthCheckPort, ReadinessProbeInitialDelay),
					LivenessProbe:   newLivenessProbe(healthCheckPath, HealthCheckPort, LivenessProbeInitialDelay),
					ImagePullPolicy: getPullPolicy(),
					Ports:           []core.ContainerPort{{Name: "http", ContainerPort: HttpPort, Protocol: "TCP"}},
					SecurityContext: &core.SecurityContext{ReadOnlyRootFilesystem: Bool(true)},
				},
			},
		},
	}

	if len(component.Volumes) > 0 {
		pod = *addPersistentVolumes(component, &pod)
	}

	// always add one writable emptyDir volume
	pod = *addEmptyDirVolume(&pod, WritableVolumeName, WritableVolumeMountPath)

	// always add one writable/run emptyDir volume
	pod = *addEmptyDirVolume(&pod, WritableRunVolumeName, WritableRunVolumeMountPath)

	// add volume with the service account token
	pod = *addServiceAccountTokenVolume(&pod)

	if component.Resources.Size() > 0 {
		pod.Spec.Containers[0].Resources = component.Resources
	}

	if component.EnableJpda == "true" {
		addDebuggingContainerPort(&pod.Spec.Containers[0])
	}

	if common.PodPriorityClassesEnabled() {
		pod.Spec.PriorityClassName = getPriorityClassName(component)
	}

	if len(component.NodeSelector) > 0 {
		pod.Spec.NodeSelector = component.NodeSelector
	}

	return pod
}

func getPriorityClassName(component *tribefirev1.TribefireComponent) string {
	switch component.Type {
	case tribefirev1.Services:
		return TribefireMasterPriorityClassName
	case tribefirev1.Cartridge:
		return TribefireCartridgePriorityClassName
	default:
		return TribefireComponentPriorityClassName
	}
}

func addDebuggingContainerPort(container *core.Container) {
	jpdaPort := core.ContainerPort{Name: "jpda", ContainerPort: JpdaPort, Protocol: "TCP"}
	container.Ports = append(container.Ports, jpdaPort)
}

func addPersistentVolumes(component *tribefirev1.TribefireComponent, pod *core.PodTemplateSpec) *core.PodTemplateSpec {
	for _, tfVolume := range component.Volumes {
		volume := core.Volume{
			Name: tfVolume.Name,
			VolumeSource: core.VolumeSource{
				PersistentVolumeClaim: &core.PersistentVolumeClaimVolumeSource{
					ReadOnly:  false,
					ClaimName: tfVolume.VolumeClaimName,
				},
			},
		}

		mount := core.VolumeMount{
			Name:      tfVolume.Name,
			MountPath: tfVolume.VolumeMountPath,
		}

		// todo currently we only mount the additional volume in the first container
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, mount)
		pod.Spec.Volumes = append(pod.Spec.Volumes, volume)
	}

	return pod
}

func addEmptyDirVolume(pod *core.PodTemplateSpec, volumeName, mountPath string) *core.PodTemplateSpec {
	// Create the volume
	emptyDirVolume := core.Volume{
		Name: volumeName,
		VolumeSource: core.VolumeSource{
			EmptyDir: &core.EmptyDirVolumeSource{},
		},
	}

	// Add volume to pod template spec
	if pod.Spec.Volumes == nil {
		pod.Spec.Volumes = []core.Volume{}
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, emptyDirVolume)

	// Add volume mount to all containers in the pod template
	for i := range pod.Spec.Containers {
		volumeMount := core.VolumeMount{
			Name:      volumeName,
			MountPath: mountPath,
		}
		pod.Spec.Containers[i].VolumeMounts = append(
			pod.Spec.Containers[i].VolumeMounts,
			volumeMount,
		)
	}

	return pod
}

func addServiceAccountTokenVolume(pod *core.PodTemplateSpec) *core.PodTemplateSpec {
	defaultMode := int32(420) // 0644 in octal
	expSeconds := int64(3599)

	projectedVolume := core.Volume{
		Name: "kube-api-access",
		VolumeSource: core.VolumeSource{
			Projected: &core.ProjectedVolumeSource{
				DefaultMode: &defaultMode,
				Sources: []core.VolumeProjection{
					{
						ServiceAccountToken: &core.ServiceAccountTokenProjection{
							ExpirationSeconds: &expSeconds,
							Path:              "token",
						},
					},
					{
						ConfigMap: &core.ConfigMapProjection{
							LocalObjectReference: core.LocalObjectReference{
								Name: "kube-root-ca.crt",
							},
							Items: []core.KeyToPath{
								{
									Key:  "ca.crt",
									Path: "ca.crt",
								},
							},
						},
					},
					{
						DownwardAPI: &core.DownwardAPIProjection{
							Items: []core.DownwardAPIVolumeFile{
								{
									Path: "namespace",
									FieldRef: &core.ObjectFieldSelector{
										APIVersion: "v1",
										FieldPath:  "metadata.namespace",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Add volume to pod template spec
	if pod.Spec.Volumes == nil {
		pod.Spec.Volumes = []core.Volume{}
	}
	pod.Spec.Volumes = append(pod.Spec.Volumes, projectedVolume)

	// Add volume mount to all containers in the pod template
	volumeMount := core.VolumeMount{
		Name:      "kube-api-access",
		MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
		ReadOnly:  true,
	}

	for i := range pod.Spec.Containers {
		pod.Spec.Containers[i].VolumeMounts = append(
			pod.Spec.Containers[i].VolumeMounts,
			volumeMount,
		)
	}

	return pod
}

func getHealthCheckPath(component *tribefirev1.TribefireComponent, healthCheckUri string) string {
	if customHealthCheckUri := common.CustomCartridgeHealthCheckPath(); component.Type == tribefirev1.Cartridge && customHealthCheckUri != "" {

		healthCheckUri = customHealthCheckUri
	}

	if customHealthCheckPath := component.CustomHealthCheckPath; customHealthCheckPath != "" {
		healthCheckUri = customHealthCheckPath
	}

	return healthCheckUri
}

func getReadinessCheckPath(component *tribefirev1.TribefireComponent, readinessCheckUri string) string {
	if customReadinessCheckUri := common.CustomCartridgeReadinessCheckPath(); component.Type == tribefirev1.Cartridge && customReadinessCheckUri != "" {

		readinessCheckUri = customReadinessCheckUri
	}

	return readinessCheckUri
}

func Bool(b bool) *bool {
	return &b
}
