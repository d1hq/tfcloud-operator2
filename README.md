# tribefire-cloud

This repository contains a Kubernetes operator that manages `TribefireRuntime` deployments. A `TribefireRuntime` includes
usually several components such as the `Explorer` or `WebReader` components, but in any case a `TribefireMaster`.
This operator was built with [Kubebuilder v3.7.0](https://github.com/kubernetes-sigs/kubebuilder)

## Tools needed

The setup procedure depends on a couple of external tools:

* [go](https://go.dev/doc/install)
* [Helm 3.9+](https://helm.sh/docs/intro/install/)
* [aws cli 2.9.5+](https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html)
* Install dependencies using the `Makefile`

    ```shell
    make kustomize
    make envtest
    make controller-gen
    ```

* Prepare Docker credentials

    ```shell
    DOCKER_REGISTRY_HOST=docker.example.com
    DOCKER_REGISTRY_USER=example-user
    DOCKER_REGISTRY_PASSWORD=example-password

    echo -n "${DOCKER_REGISTRY_USER}" > config/manager/secrets/username.txt
    echo -n "${DOCKER_REGISTRY_PASSWORD}" > config/manager/secrets/password.txt
    echo '{
        "auths": {
            "'${DOCKER_REGISTRY_HOST}'": {
            "auth": "'$(echo -n "${DOCKER_REGISTRY_USER}:${DOCKER_REGISTRY_PASSWORD}" | base64 --wrap=0 -)'"
            }
        }
    }' > config/manager/secrets/.dockerconfigjson
    ```

  * or use template files in `./config/manager/secrets`

## Update / Migration

Check [Migration](MIGRATION.md) doc for update instructions.

## Getting Started

The entire deployment process is driven by various `make` targets. The provided Makefile is an extension to the original
Makefile that is part of a kubebuilder project (see [this](https://github.com/kubernetes-sigs/kubebuilder/blob/master/pkg/plugins/golang/v4/scaffolds/internal/templates/makefile.go)
for details) and features additional targets for

* debugging using delve and pyroscope via `make exec-debug` and `make deploy-pyroscope`
* rendering manifests via `make render-manifests`
* deploying prerequisites (traefik and cert-manager) via `make deploy-prerequisites`

The first step in setting up a Kubernetes cluster for the operator is to deploy the pre-requisites.

### Deploy Prerequisites

Currently there are two main pre-requisites which are deployed globally:

* `cert-manager` for managing TLS certs primarily for the admission webhooks
* `traefik` for managing ingress routes

Both prerequisites are deployed using their official helm charts. The options that are used for deploying traefik and
cert-manager can be checked in their specific make targets, `deploy-cert-manager` and `deploy-traefik`. For traefik,
there is a [values.yaml](hack/traefik-helm-values.yaml) file that is used to control the options, it can be found in the `hack` folder.

To install the prerequisites, run the corresponding `make` target:

```shell
make deploy-prerequisites
```

Two namespaces will be created (`traefik` and `cert-manager`). After the helm charts have been installed successfully,
you can check that the pods are running in the corresponding namespaces:

```shell
kubectl get pods -n traefik

NAME                       READY   STATUS    RESTARTS        AGE
traefik-6bb4cbbc89-qrx9k   1/1     Running   0               3d15h
```

```shell
kubectl get pods -n cert-manager

NAME                                       READY   STATUS    RESTARTS        AGE
cert-manager-66dbc9658d-7n45c              1/1     Running   0               3d15h
cert-manager-cainjector-69cfd4dbc9-jkfdg   1/1     Running   0               3d15h
cert-manager-webhook-5f454c484c-lkwhs      1/1     Running   0               3d15h
```

Traefik will open port `30080/TCP` for network traffic and `30880/TCP` for healthchecks, use the endpoint `/ping`:

```shell
kubectl get svc -n traefik

NAME      TYPE       CLUSTER-IP       EXTERNAL-IP   PORT(S)                        AGE
traefik   NodePort   172.20.216.206   <none>        30880:30880/TCP,80:30080/TCP   99m
```

### Deploy the etcd-operator

The location of the etcd-operator image is configurable, see the variable `ETCD_OPERATOR_IMAGE` in `Makefile`. Etcd-operator is deployed as one instance and manages all etcd clusters within the k8s cluster. This can be adjusted by removing the `-clusterwide` argument if needed.

The deployment assumes that a valid `config/manager/secrets/.dockerconfigjson` exists, this credential be used to authenticate to the Docker registry for image pull.

### Deploy Operator

Similar to the prerequisites, tribefire-cloud operators are deployed via the provided `Makefile`. This means that the previously used `deploy-operator.sh` script is not used anymore. Check the header of the `Makefile` for configurable environment variables. These control the location of images used in the deployment, adjust them to your needs.

When deploying the operator using the target `deploy`, an `etcd` cluster will be provisioned into the desired namespace. For that, the
[etcd operator](https://github.com/on2itsecurity/etcd-operator) is used. Our configuration is running in a stateful set with 3 replicas and has persistence and RBAC disabled. You can control all options via the corresponding
[manifest](config/etcd/tf-etcd-cluster.yaml) file.

The namespace that will be used to deploy the etcd cluster as well as the operator can be set via `OPERATOR_NAMESPACE`.

Deploying an operator and an etcd cluster in the `adx` namespace is then done via

```shell
OPERATOR_NAMESPACE=adx make deploy
```

The `Makefile` also includes a separate `deploy-etcd` target. This is called automatically when deploying the operator using the `deploy` target and usually is not needed. Deploying only the etcd in the `adx` namespace can be done via:

```shell
OPERATOR_NAMESPACE=adx make deploy-etcd
```

After a few seconds you should see the etcd pods up and running:

```shell
kubectl get pods -n adx
NAME               READY   STATUS    RESTARTS   AGE
etcd-tribefire-0   1/1     Running   0          106s
etcd-tribefire-1   1/1     Running   0          106s
etcd-tribefire-2   1/1     Running   0          106s
```

If you need to changes pull image secrets (e.g. for Artifactory) you need to create the following files in the
[manager config directory](config/manager/secrets):

* `username.txt` contains the Docker registry username
* `password.txt` contains the Docker registry password
* `.dockerconfigjson` contains the Docker registry auth information

Please see the `.template` files in the secrets directory for examples.

The output will look similar to this:

```shell
OPERATOR_DOCKER_HOST="your.docker.host" \
    OPERATOR_NAMESPACE=adx make deploy

test -s /path/to/tribefire-cloud/bin/controller-gen || GOBIN=/path/to/tribefire-cloud/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.9.2
/path/to/tribefire-cloud/bin/controller-gen rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
cd config/manager && /opt/homebrew/bin/kustomize edit set image controller=dockerregistry.example.com/tribefire-cloud/operator-development:0.9.0-alpha10
cd config/default && /opt/homebrew/bin/kustomize edit set namespace adx
cd config/default && /opt/homebrew/bin/kustomize edit set nameprefix tfcloud-adx-
/opt/homebrew/bin/kustomize build config/default | kubectl apply -f -
Warning: resource namespaces/adx is missing the kubectl.kubernetes.io/last-applied-configuration annotation which is required by kubectl apply. kubectl apply should only be used on resources created declaratively by either kubectl create --save-config or kubectl apply. The missing annotation will be patched automatically.
namespace/adx configured
customresourcedefinition.apiextensions.k8s.io/tribefireruntimes.tribefire.cloud configured
serviceaccount/tfcloud-adx-controller-manager created
role.rbac.authorization.k8s.io/tfcloud-adx-leader-election-role created
clusterrole.rbac.authorization.k8s.io/tfcloud-adx-manager-role created
rolebinding.rbac.authorization.k8s.io/tfcloud-adx-leader-election-rolebinding created
clusterrolebinding.rbac.authorization.k8s.io/tfcloud-adx-manager-rolebinding created
configmap/tfcloud-adx-operator-config-map-ckt9b75md9 created
secret/tfcloud-adx-bt-artifactory-5b9g7h7ht9 created
secret/tfcloud-adx-bt-artifactory-bootstrap-7bk82c2f5f created
service/tfcloud-adx-webhook-service created
deployment.apps/tfcloud-adx-controller-manager created
certificate.cert-manager.io/tfcloud-adx-serving-cert created
issuer.cert-manager.io/tfcloud-adx-selfsigned-issuer created
mutatingwebhookconfiguration.admissionregistration.k8s.io/tfcloud-adx-mutating-webhook-configuration created
validatingwebhookconfiguration.admissionregistration.k8s.io/tfcloud-adx-validating-webhook-configuration created
```

The deployment itself is built on `kustomize`. All relevant manifests are located in the [config](config/) folder. Please
note that many of those manifests are auto-generated by
[controller-gen CLI](https://github.com/kubernetes-sigs/kubebuilder/blob/master/docs/book/src/reference/controller-gen.md).
This means that changes you make to most of the files in the config directory will be overwritten once you run `make deploy`
as it also calls the `manifests` target that invokes `controller-gen`. The content of the manifests are largely controlled
via annotations in the Go code, for instance in the [tribefire runtime types](api/v1/tribefireruntime_types.go). Whatever
needs to be changed in the manifests after controller-gen has run needs to be handled via kustomize patches, see
[webhook patches](config/webhook/kustomization.yaml) for an example how to do that via JSON patches.

Verify that the operator deployment, secrets and configmaps are deployed correctly:

```shell
kubectl get deployments -n adx

NAME                             READY   UP-TO-DATE   AVAILABLE   AGE
tfcloud-adx-controller-manager   1/1     1            1           14m
```

```shell
kubectl get secrets -n adx

NAME                                              TYPE                                  DATA   AGE
default-token-x792c                               kubernetes.io/service-account-token   3      15m
etcd-tribefire-jwt-token                          Opaque                                1      15m
sh.helm.release.v1.etcd-tribefire.v1              helm.sh/release.v1                    1      15m
tfcloud-adx-bt-artifactory-5b9g7h7ht9             kubernetes.io/dockerconfigjson        1      13m
tfcloud-adx-bt-artifactory-bootstrap-7bk82c2f5f   Opaque                                2      13m
tfcloud-adx-controller-manager-token-zj924        kubernetes.io/service-account-token   3      13m
webhook-server-cert                               kubernetes.io/tls                     3      13m
```

```shell
kubectl get configmap -n adx

NAME                                         DATA   AGE
kube-root-ca.crt                             1      16m
tfcloud-adx-operator-config-map-ckt9b75md9   13     14m
```

### Build the operator

1. Make changes to the codebase
2. Run go tests:

    ```shell
    make test

    test -s /path/to/tribefire-cloud/bin/controller-gen || GOBIN=/path/to/tribefire-cloud/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.9.2
    /path/to/tribefire-cloud/bin/controller-gen rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
    /path/to/tribefire-cloud/bin/controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."
    go fmt ./...
    go vet ./...
    test -s /path/to/tribefire-cloud/bin/setup-envtest || GOBIN=/path/to/tribefire-cloud/bin go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
    KUBEBUILDER_ASSETS="/path/to/tribefire-cloud/bin/k8s/1.25.0-darwin-arm64" go test ./... -coverprofile cover.out
    ?   	tribefire-operator	[no test files]
    ok  	tribefire-operator/api/v1	0.575s	coverage: 0.4% of statements
    ?   	tribefire-operator/common	[no test files]
    ok  	tribefire-operator/controllers	3.364s	coverage: 51.5% of statements
    ?   	tribefire-operator/providers	[no test files]
    ok  	tribefire-operator/tribefire	0.238s	coverage: 41.3% of statements
    ?   	tribefire-operator/validation	[no test files]
    ```

3. Build the operator binary

    ```shell
    make build

    test -s /path/to/tribefire-cloud/bin/controller-gen || GOBIN=/path/to/tribefire-cloud/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.9.2
    /path/to/tribefire-cloud/bin/controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."
    go fmt ./...
    go vet ./...
    go build -gcflags "all=-N -l" -o bin/manager main.go
    ```

4. Build and push your image to the location specified using `OPERATOR_IMAGE` and `OPERATOR_TAG` (found in Makefile):

```shell
make docker-build docker-push
```

### Install CRDs

The tribefire-runtime CRD is automatically generated base on the annotations in the
[tribefire_runtime.go](api/v1/tribefireruntime_types.go) file. The `controller-gen` tool generates the corresponding YAML
in the relevant [config directory](config/crd/bases).

To install the CRDs to the cluster:

```shell
make install
```

### Uninstall CRDs

To delete the CRDs from the cluster:

```shell
make uninstall
```

### Undeploy operator

To undeploy the operator from the cluster:

```shell
make undeploy
```

One can undeploy operator from a certain namespace with command below. **NOTICE** this will also remove the namespace, so save secretes/etc! Remeber also to remove any Tribefire deployments before undeploying the operator.

```sh
OPERATOR_NAMESPACE=adx make undeploy
```

### How it works

This project aims to follow the Kubernetes [Operator pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)

It uses [Controllers](https://kubernetes.io/docs/concepts/architecture/controller/)
which provides a reconcile function responsible for synchronizing resources until the desired state is reached on the cluster

### Test It Out

1. Install the CRDs into the cluster:

    ```sh
    make install
    ```

2. Run your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

    ```shell
    make run
    ```

3. Debug your controller (this will run in the foreground, so switch to a new terminal if you want to leave it running):

    ```shell
    make exec-debug
    test -s /path/to/tribefire-cloud/bin/controller-gen || GOBIN=/path/to/tribefire-cloud/bin go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.9.2
    /path/to/tribefire-cloud/bin/controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."
    go fmt ./...
    go vet ./...
    go build -gcflags "all=-N -l" -o bin/manager main.go
    *******************************************************************************

    Important: Make sure that you forward etcd traffic using kubectl port-forward!
            > kubectl port-forward svc/etcd-tribefire 2379 -n tribefire

    *******************************************************************************
    dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec bin/manager
    API server listening at: [::]:2345
    2022-11-21T17:03:06+01:00 warning layer=rpc Listening for remote connections (connections are not authenticated nor encrypted)
    debugserver-@(#)PROGRAM:LLDB  PROJECT:lldb-1400.0.30.3
    for arm64.
    Got a connection, launched process bin/manager (pid = 83846).
    ```

You can then initiate a go debugging session on port `2345`. Also make sure to port-forward `etcd` via:

```shell
kubectl port-forward svc/etcd-tribefire -n adx 2379
```

### Modifying the API definitions

If you are editing the API definitions, generate the manifests such as CRs or CRDs using:

```shell
make manifests
```

**NOTE:** Run `make --help` for more information on all potential `make` targets

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## Changelog

* Bug fixes, see below
* Add separate CRD deployment target
* Allow to specify different docker registry URL using the environment variable `OPERATOR_DOCKER_HOST`, see below.

## Fixed issues

* Etcd issues from 2.0 release were fixed by migrating to etcd operator. The operator does better job of keeping the etcd cluster healthy in case one of the pods is restarted.
* The issue when undeploying one namespace would undeploy all TF resources cluster-wide was fixed.

## Known issues

### Namespace stuck in `Terminating` state

Sometimes when using `make undeploy` target the namespace can get stuck in `Terminating` state. To avoid this make sure you first undeploy all `tf` resources from the namespace, use `make undeploy` only after that.

How to fix namespace stuck in terminating state:

1. Get the json definition of the namespace `kubectl get ns "your_namespace" -o json > ns.json`
1. Remove finalizers from `ns.json`, finalizers should be an empty array after this step
1. Using `kubectl proxy` finalize the namespace object `kubectl proxy & ; curl -k -H "Content-Type: application/json" -X PUT --data-binary @ns.json http://127.0.0.1:8001/api/v1/namespaces/your_namespace/finalize`
1. Check the namespace status `kubectl get ns`

### Kustomize versions

The built is tested with Kustomize v4.5.5. There are some breaking changes introduced with Kustomize 5.x which the build does not support currently.

## Traefik ingress controller

We are using internal Traefik health check with the Application Load Balancer and for that we are exposing Traefik port `30880/TCP` endpoint `/ping`. Please ensure that the port is not used for anything else except for the health check and no traffic is forwarded to it.

## Using the operator with self-hosted docker images

First configure the `OPERATOR_DOCKER_HOST` environment variable to point to the base of your docker repo, e.g. `docker.artifactory.company.com`. The Makefile `deploy` target expects to find these images:

* the operator image `$(OPERATOR_DOCKER_HOST)/tribefire-cloud/tribefire-operator:$(OPERATOR_TAG)`
* the postgres checker image `$(OPERATOR_DOCKER_HOST)/tribefire-cloud/postgres-checker:0.0.4`
