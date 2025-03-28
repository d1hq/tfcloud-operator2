package tribefire

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	tribefirev1 "tribefire-operator/api/v1"
)

func NewServiceAccount(tf *tribefirev1.TribefireRuntime) *corev1.ServiceAccount {

	serviceAccountName := buildDefaultServiceAccountName(tf)
	autoMount := false
	serviceAccount := &corev1.ServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: tf.Namespace,
		},
		AutomountServiceAccountToken: &autoMount,
	}

	addOwnerRefToObject(serviceAccount, asOwner(tf))
	dumpResourceToStdout(serviceAccount)

	return serviceAccount
}

func NewRole(tf *tribefirev1.TribefireRuntime) *rbacv1.Role {
	roleName := buildDefaultRoleName(tf)

	role := &rbacv1.Role{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Role",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleName,
			Namespace: tf.Namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"endpoints"},
				Verbs:     []string{"get", "list"},
			},
		},
	}

	addOwnerRefToObject(role, asOwner(tf))
	dumpResourceToStdout(role)

	return role
}

func NewRoleBinding(tf *tribefirev1.TribefireRuntime) *rbacv1.RoleBinding {
	roleBindingName := buildDefaultRoleBindingName(tf)

	roleBinding := &rbacv1.RoleBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RoleBinding",
			APIVersion: "rbac.authorization.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleBindingName,
			Namespace: tf.Namespace,
		},
		RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: buildDefaultRoleName(tf)},
		Subjects: []rbacv1.Subject{
			{
				Namespace: tf.Namespace,
				Name:      buildDefaultServiceAccountName(tf),
				Kind:      "ServiceAccount",
			},
		},
	}

	addOwnerRefToObject(roleBinding, asOwner(tf))
	dumpResourceToStdout(roleBinding)

	return roleBinding
}

func buildDefaultRoleBindingName(tf *tribefirev1.TribefireRuntime) string {
	return tf.Name

}

func buildDefaultRoleName(tf *tribefirev1.TribefireRuntime) string {
	return tf.Name
}

func buildDefaultServiceAccountName(tf *tribefirev1.TribefireRuntime) string {
	return tf.Name
}
