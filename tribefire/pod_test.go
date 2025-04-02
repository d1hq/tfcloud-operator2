package tribefire

import (
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"os"
	"testing"
	tribefirev1 "tribefire-operator/api/v1"
)

func TestCreatePod(t *testing.T) {
	tf, component := createDemoRuntime()
	podSpec := createPod(tf, component, nil, MasterAppName, "/")
	assert.Equal(t, "test-"+MasterAppName, podSpec.Name)
	assert.Equal(t, "demo/demo:1.1.1", podSpec.Spec.Containers[0].Image)
}

func TestCreatePodWithVolumeMounts(t *testing.T) {
	tf, component := createDemoRuntime()
	volume := &tribefirev1.TribefireVolume{}
	volume.Name = "test-volume"
	volume.VolumeMountPath = "/test"
	volume.VolumeClaimName = "test-pvc"
	component.Volumes = append(component.Volumes, *volume)
	podSpec := createPod(tf, component, nil, MasterAppName, "/")
	assert.Equal(t, "test-"+MasterAppName, podSpec.Name)
	assert.Equal(t, "demo/demo:1.1.1", podSpec.Spec.Containers[0].Image)
	assert.Equal(t, 4, len(podSpec.Spec.Volumes)) //3 volumes are created by default - /writable, /writable/run and service account token
	assert.Equal(t, volume.Name, podSpec.Spec.Volumes[0].Name)
	assert.Equal(t, volume.VolumeClaimName, podSpec.Spec.Volumes[0].PersistentVolumeClaim.ClaimName)
	assert.Equal(t, volume.Name, podSpec.Spec.Containers[0].VolumeMounts[0].Name)
	assert.Equal(t, volume.VolumeMountPath, podSpec.Spec.Containers[0].VolumeMounts[0].MountPath)
}

func createDemoRuntime() (*tribefirev1.TribefireRuntime, *tribefirev1.TribefireComponent) {
	tf := &tribefirev1.TribefireRuntime{}
	tf.Name = "test"
	tf.Namespace = "test"
	component := &tribefirev1.TribefireComponent{}
	component.Name = "master"
	component.Image = "demo/demo"
	component.ImageTag = "1.1.1"
	tf.Spec.Components = append(tf.Spec.Components, *component)
	return tf, component
}

func TestCreatePodWithResourceConstraints(t *testing.T) {
	tf, component := createDemoRuntime()
	requests := v1.ResourceList{}
	limits := v1.ResourceList{}
	cpuRequest, _ := resource.ParseQuantity("500mi")
	cpuLimit, _ := resource.ParseQuantity("1000mi")
	memRequest, _ := resource.ParseQuantity("512m")
	memLimit, _ := resource.ParseQuantity("1G")

	requests["cpu"] = cpuRequest
	requests["memory"] = memRequest

	limits["cpu"] = cpuLimit
	limits["memory"] = memLimit

	component.Resources = v1.ResourceRequirements{Limits: limits, Requests: requests}

	podSpec := createPod(tf, component, nil, MasterAppName, "/")

	assert.Equal(t, cpuRequest.String(), podSpec.Spec.Containers[0].Resources.Requests.Cpu().String())
	assert.Equal(t, cpuLimit.String(), podSpec.Spec.Containers[0].Resources.Limits.Cpu().String())
	assert.Equal(t, memRequest.String(), podSpec.Spec.Containers[0].Resources.Requests.Memory().String())
	assert.Equal(t, memLimit.String(), podSpec.Spec.Containers[0].Resources.Limits.Memory().String())
}

func TestCreatePodWithDebugSettings(t *testing.T) {
	tf, component := createDemoRuntime()
	component.EnableJpda = "true"
	podSpec := createPod(tf, component, nil, MasterAppName, "/")

	assert.Equal(t, 2, len(podSpec.Spec.Containers[0].Ports))
	assert.EqualValues(t, 8000, podSpec.Spec.Containers[0].Ports[1].ContainerPort)
	assert.Equal(t, "jpda", podSpec.Spec.Containers[0].Ports[1].Name)
}

func TestCreatePodWithCustomReadinessCheckPath(t *testing.T) {
	tf, component := createDemoRuntime()
	component.Type = tribefirev1.Cartridge
	_ = os.Setenv("CUSTOM_CARTRIDGE_READINESS_CHECK_URI", "/healthz?scope=hardwired")

	podSpec := createPod(tf, component, nil, "SimpleCartridge", "/")
	assert.Equal(t, "/healthz?scope=hardwired", podSpec.Spec.Containers[0].ReadinessProbe.HTTPGet.Path)
	assert.Equal(t, "/", podSpec.Spec.Containers[0].LivenessProbe.HTTPGet.Path)
}

func TestCreatePodWithCustomHealthCheckPath(t *testing.T) {
	tf, component := createDemoRuntime()
	component.Type = tribefirev1.Cartridge
	_ = os.Setenv("CUSTOM_CARTRIDGE_READINESS_CHECK_URI", "/healthz?scope=hardwired")
	_ = os.Setenv("CUSTOM_CARTRIDGE_HEALTH_CHECK_URI", "/healthz?scope=all")

	podSpec := createPod(tf, component, nil, "SimpleCartridge", "/")
	assert.Equal(t, "/healthz?scope=all", podSpec.Spec.Containers[0].LivenessProbe.HTTPGet.Path)
	assert.Equal(t, "/healthz?scope=hardwired", podSpec.Spec.Containers[0].ReadinessProbe.HTTPGet.Path)
}

func TestCreatePodDefaultVolumes(t *testing.T) {
	tf, component := createDemoRuntime()
	podSpec := createPod(tf, component, nil, MasterAppName, "/")

	// Check total number of volumes
	assert.Equal(t, 3, len(podSpec.Spec.Volumes), "Should have exactly 3 default volumes")

	// Check volume mounts in container
	container := podSpec.Spec.Containers[0]
	writableVolumeMounts := 0
	for _, mount := range container.VolumeMounts {
		if mount.ReadOnly == false {
			writableVolumeMounts++
		}
	}
	assert.Equal(t, 2, writableVolumeMounts, "Should have exactly 2 writable volume mounts")
}
