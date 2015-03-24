package strategy

import (
	"path/filepath"

	kapi "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/golang/glog"
	buildapi "github.com/openshift/origin/pkg/build/api"
	imageapi "github.com/openshift/origin/pkg/image/api"
)

// dockerSocketPath is the default path for the Docker socket inside the builder
// container
const (
	dockerSocketPath      = "/var/run/docker.sock"
	dockerSecretMountPath = "/var/run/secrets"
)

// setupDockerSocket configures the pod to support the host's Docker socket
func setupDockerSocket(podSpec *kapi.Pod) {
	dockerSocketVolume := kapi.Volume{
		Name: "docker-socket",
		VolumeSource: kapi.VolumeSource{
			HostPath: &kapi.HostPathVolumeSource{
				Path: dockerSocketPath,
			},
		},
	}

	dockerSocketVolumeMount := kapi.VolumeMount{
		Name:      "docker-socket",
		MountPath: dockerSocketPath,
	}

	podSpec.Spec.Volumes = append(podSpec.Spec.Volumes,
		dockerSocketVolume)
	podSpec.Spec.Containers[0].VolumeMounts =
		append(podSpec.Spec.Containers[0].VolumeMounts,
			dockerSocketVolumeMount)
}

// setupBuildEnv injects human-friendly environment variables which provides
// useful information about the current build.
func setupBuildEnv(build *buildapi.Build, pod *kapi.Pod) error {
	vars := []kapi.EnvVar{}

	switch build.Parameters.Source.Type {
	case buildapi.BuildSourceGit:
		vars = append(vars, kapi.EnvVar{Name: "SOURCE_URI", Value: build.Parameters.Source.Git.URI})
		vars = append(vars, kapi.EnvVar{Name: "SOURCE_REF", Value: build.Parameters.Source.Git.Ref})
	default:
		// Do nothing for unknown source types
	}

	ref, err := imageapi.ParseDockerImageReference(build.Parameters.Output.DockerImageReference)
	if err != nil {
		return err
	}
	vars = append(vars, kapi.EnvVar{Name: "OUTPUT_REGISTRY", Value: ref.Registry})
	ref.Registry = ""
	vars = append(vars, kapi.EnvVar{Name: "OUTPUT_IMAGE", Value: ref.String()})

	if len(pod.Spec.Containers) > 0 {
		pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, vars...)
	}
	return nil
}

// setupDockerSecrets mounts Docker Registry secrets into Pod running the build,
// allowing Docker to authenticate against private registries or Docker Hub.
func setupDockerSecrets(pod *kapi.Pod, pushSecret string) {
	if len(pushSecret) == 0 {
		return
	}

	volume := kapi.Volume{
		Name: pushSecret,
		VolumeSource: kapi.VolumeSource{
			Secret: &kapi.SecretVolumeSource{
				Target: kapi.ObjectReference{
					Kind: "Secret",
					Name: pushSecret,
					// TODO: The namespace should not be required here.
					//       See: https://github.com/GoogleCloudPlatform/kubernetes/pull/5807
					Namespace: pod.Namespace,
				},
			},
		},
	}
	volumeMount := kapi.VolumeMount{
		Name:      pushSecret,
		MountPath: filepath.Join(dockerSecretMountPath, "push"),
		ReadOnly:  true,
	}

	glog.V(3).Infof("Adding %s secret to build Pod %s", pushSecret, pod.Name)
	pod.Spec.Volumes = append(pod.Spec.Volumes, volume)
	pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, volumeMount)

	// TODO: The push && pull secrets are now the same, but the pull secret should
	//       be delivered by Service Account in future.
	// TODO: The Secret.Name must match with one of the Secret.Data[] keys in
	//       in order to provide the full path to dockercfg. If it does not, then
	//       the builder fail to find the dockercfg
	pod.Spec.Containers[0].Env = append(pod.Spec.Containers[0].Env, []kapi.EnvVar{
		{Name: "PUSH_DOCKERCFG_PATH", Value: filepath.Join(volumeMount.MountPath, pushSecret)},
		{Name: "PULL_DOCKERCFG_PATH", Value: filepath.Join(volumeMount.MountPath, pushSecret)},
	}...)
}

// mergeEnvWithoutDuplicates merges two environment lists without having
// duplicate items in the output list.
func mergeEnvWithoutDuplicates(source []kapi.EnvVar, output *[]kapi.EnvVar) {
	type sourceMapItem struct {
		index int
		value string
	}
	// Convert source to Map for faster access
	sourceMap := make(map[string]sourceMapItem)
	for i, env := range source {
		sourceMap[env.Name] = sourceMapItem{i, env.Value}
	}
	result := *output
	for i, env := range result {
		// If the value exists in output, override it and remove it
		// from the source list
		if v, found := sourceMap[env.Name]; found {
			result[i].Value = v.value
			source = append(source[:v.index], source[v.index+1:]...)
		}
	}
	*output = append(result, source...)
}

// getContainerVerbosity returns the defined BUILD_LOGLEVEL value
func getContainerVerbosity(containerEnv []kapi.EnvVar) (verbosity string) {
	for _, env := range containerEnv {
		if env.Name == "BUILD_LOGLEVEL" {
			verbosity = env.Value
			break
		}
	}
	return
}
