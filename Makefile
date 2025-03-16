
# Image URL to use all building/pushing image targets
#OPERATOR_IMAGE = dockerregistry.example.com/tribefire-cloud/operator-development
OPERATOR_DOCKER_HOST ?= docker.artifactory.braintribe.com
#OPERATOR_DOCKER_HOST = 204787864596.dkr.ecr.eu-central-1.amazonaws.com
OPERATOR_IMAGE ?= $(OPERATOR_DOCKER_HOST)/tribefire-cloud/tribefire-operator

OPERATOR_IMAGE_DBG =$(OPERATOR_DOCKER_HOST)/tribefire-cloud/tribefire-operator-dbg
OPERATOR_TAG = 2.2

IMG ?= $(OPERATOR_IMAGE):$(OPERATOR_TAG)
# ddocker340/postgres:16.8-alpine3.21-20250314 is based on postgres:16.8-alpine3.21
# libxml2 package has been updated in the image.
TRIBEFIRE_POSTGRESQL_IMAGE ?= ddocker340/postgres:16.8-alpine3.21-20250314
TRIBEFIRE_POSTGRESQL_CHECKER_IMAGE ?= $(OPERATOR_DOCKER_HOST)/tribefire-cloud/postgres-checker:1.1
ETCD_OPERATOR_IMAGE ?= $(OPERATOR_DOCKER_HOST)/tribefire-cloud/etcd-operator:20250312-3983c32

# NAME_PREFIX is pre-prepended to all operator related resources, but not to tribefire resources
# such as the tribefire-master
OPERATOR_NAMESPACE ?= tribefire
OPERATOR_NAME_PREFIX ?= tfcloud-

ENVTEST_K8S_VERSION = 1.25.0
TRAEFIK_VERSION=27.0.2 #If you change this make sure you update CRD too

TRAEFIK_NAMESPACE ?= traefik
CERTMANAGER_NAMESPACE ?= cert-manager
PYROSCOPE_NAMESPACE ?= pyroscope

DEMO_DEPLOYMENT_YAML = samples/demo2.yaml

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test ./... -coverprofile cover.out

##@ Build

.PHONY: build
build: generate fmt vet ## Build manager binary.
	go build -gcflags "all=-N -l" -o bin/manager main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./main.go

.PHONY: set-debug-env
set-debug-env:
	@if [ -f .env ]; then \
		export `cat .env | xargs`; \
	else \
		echo "no .env file found for setting up operator environment"; \
		exit 1; \
	fi

PHONY: unset-debug-env
unset-debug-env:
	@if [ -f .env ]; then \
		unset $(cat .env | cut -d '=' -f1) ; \
	else \
		echo "no .env file found"; \
		exit 1; \
	fi

.PHONY: exec-debug
exec-debug: build set-debug-env
	@echo "*******************************************************************************" ;\
	echo "" ;\
	echo "Important: Make sure that you forward etcd traffic using kubectl port-forward!" ;\
	echo "           > kubectl port-forward svc/etcd-tribefire 2379 -n $(OPERATOR_NAMESPACE)" ;\
	echo "" ;\
	echo "*******************************************************************************";
	dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec bin/manager

# If you wish built the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64 ). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# Setting env var DOCKER_DEFAULT_PLATFORM=linux/amd64 should also work
.PHONY: docker-build
docker-build: test ## Build docker image with the manager.
	docker build -t ${IMG} --platform linux/amd64  .
#	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

# PLATFORMS defines the target platforms for  the manager image be build to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - able to use docker buildx . More info: https://docs.docker.com/build/buildx/
# - have enable BuildKit, More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image for your registry (i.e. if you do not inform a valid value via IMG=<myregistry/image:<tag>> than the export will fail)
# To properly provided solutions that supports more than one platform you should use this option.
# PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
PLATFORMS ?= linux/arm64,linux/amd64
.PHONY: docker-buildx
docker-buildx: test ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- docker buildx create --name project-v3-builder
	docker buildx use project-v3-builder
	- docker buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross
	- docker buildx rm project-v3-builder
	rm Dockerfile.cross

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: pre-deploy-undeploy
pre-deploy-undeploy:
## this sed approach is far from ideal. I tried using kustomize but this: https://github.com/kubernetes-sigs/kustomize/issues/4731
	cd config/manager && sed -e "s|@@dockerhost@@|$(OPERATOR_DOCKER_HOST)|g; s|@@postgresimage@@|$(TRIBEFIRE_POSTGRESQL_IMAGE)|g; s|@@postgrescheckerimage@@|$(TRIBEFIRE_POSTGRESQL_CHECKER_IMAGE)|g" < operator.properties.template > operator.properties
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	cd config/default && $(KUSTOMIZE) edit set namespace $(OPERATOR_NAMESPACE)
	cd config/default && $(KUSTOMIZE) edit set nameprefix $(OPERATOR_NAME_PREFIX)$(OPERATOR_NAMESPACE)-

.PHONY: deploy
deploy: manifests pre-deploy-undeploy ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: deploy-dry-run
deploy-dry-run: manifests pre-deploy-undeploy ## Dry-run deploy controller to the K8s cluster specified in ~/.kube/config.
	$(info ### deploy-dry-run)
	$(info This is a dry run and prints out what Kubernetes YAMLs would be applied.)
	$(KUSTOMIZE) build config/default | cat

.PHONY: render-manifests
render-manifests: pre-deploy-undeploy
	$(KUSTOMIZE) build config/default

# Undeploy controller from the K8s cluster specified in ~/.kube/config.
# Call with ignore-not-found=true to ignore resource not found errors during deletion.
.PHONY: undeploy
undeploy: pre-deploy-undeploy
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
#KUSTOMIZE ?= /opt/homebrew/bin/kustomize
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest

## Tool Versions
KUSTOMIZE_VERSION ?= v4.5.5
CONTROLLER_TOOLS_VERSION ?= v0.16.5

KUSTOMIZE_INSTALL_SCRIPT ?= "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh"
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	test -s $(LOCALBIN)/kustomize || { curl -Ss $(KUSTOMIZE_INSTALL_SCRIPT) | bash -s -- $(subst v,,$(KUSTOMIZE_VERSION)) $(LOCALBIN); }

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
$(ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest


.PHONY: deploy-cert-manager
deploy-cert-manager:
	helm repo add --force-update jetstack https://charts.jetstack.io
	helm repo update
	helm upgrade --install cert-manager jetstack/cert-manager \
      --namespace $(CERTMANAGER_NAMESPACE) \
      --create-namespace \
      --version v1.12.16 \
      --set installCRDs=true

.PHONY: deploy-cert-manager
undeploy-cert-manager:
	helm uninstall cert-manager --namespace $(CERTMANAGER_NAMESPACE)

.PHONY: deploy-traefik
deploy-traefik:
	helm repo add --force-update traefik https://traefik.github.io/charts
	helm repo update
	kubectl create namespace $(TRAEFIK_NAMESPACE) || echo "Namespace $(TRAEFIK_NAMESPACE) already exists"
	kubectl -n $(TRAEFIK_NAMESPACE) apply -f https://raw.githubusercontent.com/traefik/traefik/v2.11/docs/content/reference/dynamic-configuration/kubernetes-crd-definition-v1.yml
	kubectl -n $(TRAEFIK_NAMESPACE) apply -f https://raw.githubusercontent.com/traefik/traefik/v2.11/docs/content/reference/dynamic-configuration/kubernetes-crd-rbac.yml
	helm upgrade --install traefik traefik/traefik \
		--create-namespace --namespace $(TRAEFIK_NAMESPACE) \
		--values hack/traefik-helm-values-v27.yaml --version ${TRAEFIK_VERSION}
	kubectl apply -f hack/traefik-middleware.yaml

.PHONY: undeploy-traefik
undeploy-traefik:
	kubectl delete -f hack/traefik-middleware.yaml
	helm uninstall traefik --namespace $(TRAEFIK_NAMESPACE)

.PHONY: deploy-etcd
deploy-etcd:
	kubectl create namespace etcd-operator
	kubectl -n etcd-operator create secret docker-registry registry-secret --from-file=config/manager/secrets/.dockerconfigjson
	kubectl apply -f hack/etcd-operator/crd/etcd.database.coreos.com_etcdclusters.yaml
	sed -e "s|@@ETCD_OPERATOR_IMAGE@@|$(ETCD_OPERATOR_IMAGE)|g" < hack/etcd-operator/etcd-operator.yaml.template > hack/etcd-operator/etcd-operator.yaml
	kubectl apply -f hack/etcd-operator/etcd-operator.yaml

.PHONY: undeploy-etcd
undeploy-etcd:
	sed -e "s|@@ETCD_OPERATOR_IMAGE@@|$(ETCD_OPERATOR_IMAGE)|g" < hack/etcd-operator/etcd-operator.yaml.template > hack/etcd-operator/etcd-operator.yaml
	kubectl delete -f hack/etcd-operator/etcd-operator.yaml
	kubectl delete namespace etcd-operator
	kubectl delete -f hack/etcd-operator/crd/etcd.database.coreos.com_etcdclusters.yaml

.PHONY: undeploy-webhooks
undeploy-webhooks:
	 kubectl delete MutatingWebhookConfiguration -n $(OPERATOR_NAMESPACE)  -lapp.kubernetes.io/created-by=tribefire-cloud
	 kubectl delete ValidatingWebhookConfiguration -n $(OPERATOR_NAMESPACE)  -lapp.kubernetes.io/created-by=tribefire-cloud

.PHONY: deploy-prerequisites
deploy-prerequisites: deploy-crd deploy-cert-manager deploy-traefik deploy-etcd

.PHONY: deploy-crd
deploy-crd:
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: deploy-crd-dry-run
deploy-crd-dry-run:
	$(info ### deploy-crd-dry-run)
	$(info This is a dry run and prints out what Kubernetes YAMLs would be applied.)
	$(KUSTOMIZE) build config/crd | cat

.PHONY: undeploy-prerequisites
undeploy-prerequisites: undeploy-crd undeploy-cert-manager undeploy-traefik undeploy-etcd

.PHONY: undeploy-crd
undeploy-crd:
	$(KUSTOMIZE) build config/crd | kubectl delete -f -

.PHONY: deploy-pyroscope
deploy-pyroscope:
	helm install pyroscope pyroscope-io/pyroscope --namespace $(PYROSCOPE_NAMESPACE) \
 		--create-namespace \
 		--values hack/pyroscope-values.yaml

.PHONY: undeploy-pyroscope
undeploy-pyroscope:
	kubectl delete ns $(PYROSCOPE_NAMESPACE)

.PHONY: pyroscope-port-forward
pyroscope-port-forward:
	 kubectl port-forward svc/pyroscope -n $(PYROSCOPE_NAMESPACE) 4040

.PHONY: deploy-demo
deploy-demo:
	cat $(DEMO_DEPLOYMENT_YAML) \
		| sed 's|@@TF_NAMESPACE@@|$(OPERATOR_NAMESPACE)|' \
		| kubectl apply -f -

.PHONY: undeploy-demo
undeploy-demo:
	cat $(DEMO_DEPLOYMENT_YAML) \
		| sed 's|@@TF_NAMESPACE@@|$(OPERATOR_NAMESPACE)|' \
		| kubectl delete -f -
