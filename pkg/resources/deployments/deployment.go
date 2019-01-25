package deployments

import (   
	v1alpha1 "github.com/ajssmith/qdrouterd-operator/pkg/apis/ajssmith/v1alpha1"
  	"github.com/ajssmith/qdrouterd-operator/pkg/resources/containers"
  	"github.com/ajssmith/qdrouterd-operator/pkg/utils/selectors"
  	"github.com/ajssmith/qdrouterd-operator/pkg/utils/configs"
    appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// move this to util
// Set labels in a map
func labelsForQdrouterd(name string) map[string]string {
	return map[string]string{
           selectors.LabelAppKey: name,
           selectors.LabelResourceKey: name,
           }
}

// Create newDeploymentForCR method to create deployment
func NewDeploymentForCR(m *v1alpha1.Qdrouterd) *appsv1.Deployment {
	labels := selectors.LabelsForQdrouterd(m.Name)
	config := configs.ConfigForQdrouterd(m)
	replicas := m.Spec.Count
	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name,
			Namespace: m.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{containers.ContainerForQdrouterd(m, config)},
				},
			},
		},
	}
	volumes := []corev1.Volume{}
	for _, profile := range m.Spec.SslProfiles {
		if len(profile.Credentials) > 0 {
			volumes = append(volumes, corev1.Volume{
				Name: profile.Credentials,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: profile.Credentials,
					},
				},
			})
		}
		if len(profile.CaCert) > 0 && profile.CaCert != profile.Credentials {
			volumes = append(volumes, corev1.Volume{
				Name: profile.CaCert,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: profile.CaCert,
					},
				},
			})
		}
	}
	dep.Spec.Template.Spec.Volumes = volumes

	return dep
}
