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

package v1

import (
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	. "tribefire-operator/common"
)

// log is for logging in this package.
//var tribefireruntimelog = logf.Log.WithName("tribefireruntime-resource")

func (r *TribefireRuntime) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

//+kubebuilder:webhook:path=/mutate-tribefire-cloud-v1-tribefireruntime,mutating=true,failurePolicy=fail,sideEffects=None,groups=tribefire.cloud,resources=tribefireruntimes,verbs=create;update,versions=v1,name=mtribefireruntime.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &TribefireRuntime{}

// todo fix webhook logging currently it's 1.6684605457363176e+09	info	tf-operator	v1/tribefireruntime_webhook.go:67
// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *TribefireRuntime) Default() {
	L().Infow("default", "name", r.Name)

	_, err := SetDefaults(r)
	if err != nil {
		L().Errorf("Could not apply defaults to runtime %s: %v", r.String(), err)
		return
	}

	L().Debugf("Defaults changed Runtime")
}

//+kubebuilder:webhook:path=/validate-tribefire-cloud-v1-tribefireruntime,mutating=false,failurePolicy=fail,sideEffects=None,groups=tribefire.cloud,resources=tribefireruntimes,verbs=create;update,versions=v1,name=vtribefireruntime.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &TribefireRuntime{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *TribefireRuntime) ValidateCreate() (admission.Warnings, error) {
	L().Infow("validate create", "name", r.Name)
	return nil, nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *TribefireRuntime) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	L().Infow("validate update", "name", r.Name)
	return nil, nil
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *TribefireRuntime) ValidateDelete() (admission.Warnings, error) {
	L().Infow("validate delete", "name", r.Name)
	return nil, nil
}
