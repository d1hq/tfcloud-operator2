# Migrating to the tfcloud-operator 2.x from 0.x

## Migration procedure

1. Undeploy old traefik and etcd operator
1. Backup DB secrets from the namespace
1. Delete the namespace
1. Recreate the namespace using the `OPERATOR_NAMESPACE=ns-name make deploy` target
1. Restore DB secrets
1. Update the manifest and deploy it

## The Manifest

1. Version has changed: `apiVersion: tribefire.cloud/v1alpha1` needs to be changed to `apiVersion: tribefire.cloud/v1`
1. DCSA configuration was changed: `dcsaConfig.name` and `dcsaConfig.type` are no longer used:

    ```yaml
    dcsaConfig:
        credentialsSecretRef:
            name: database-credentials
        instanceDescriptor: jdbc:postgresql://dbhost:5432/dbname
        name: adx
        type: cloudsql
    ```

    becomes

    ```yaml
    dcsaConfig:
        credentialsSecretRef:
            name: database-credentials
        instanceDescriptor: jdbc:postgresql://dbhost:5432/dbname
    ```

## Traefik

Traefik was updated to version 2. The new version uses `middleware` instead of old rewrite rules. It is covered by `make deploy-traefik` target.

## Etcd

Etcd is set up using updated version of the etcd operator, check make target `deploy-etcd` for details.

## CRD

New CRD is generated on-the-fly when setting up a new namespace.

## CertManager

CertManager is required for management of self-signed certificates used by validating and mutating admission webhooks. It is deployed using a helm chart.

## Migration from 2.0 to 2.1

2.1 release switched back to etcd operator and brings multiple bug fixes, for more details please see the [README](README.md).

### Migration procedure (2.0 -> 2.1)

1. Backup your custom resources in the namespace, e.g. database secrets `kubectl -n namespace get secret yoursecret -o yaml > secret.yaml`
1. Backup TF resources from the namespace `kubectl -n namespace get tf -o yaml > tf.yaml`
1. Deploy etcd operator `make deploy-etcd`
1. Delete the old namespace `OPERATOR_NAMESPACE="namespace" make undeploy`. Ignore etcd errors this will produce, make sure that the namespace was deleted.
1. Create the namespace `DOCKER_HOST="your.docker.host" OPERATOR_NAMESPACE="namespace" make deploy`, make sure that etcd cluster is up `kubectl -n namespace get pods`
1. Restore backup `kubectl apply -f tf.yaml -f secret.yaml`
1. Check TF status `kubectl -n namespace get po`

## Migration from 2.1 to 2.2

### Operator version 2.2 changes

* Configurable postgres and postgres checker image location
  * this is managed by new environment variables `TRIBEFIRE_POSTGRESQL_IMAGE` and `TRIBEFIRE_POSTGRESQL_CHECKER_IMAGE`
* Add Makefile variables to control location of these images
* Update Go libraries to the latest version
* Update dependencies to maintained versions
* Update postgres-checker image
* Configurable location of the etcd-operator image
  * see the environment variable `ETCD_OPERATOR_IMAGE` in `Makefile`

### Migration procedure (2.1 -> 2.2)

There are 2 ways of updating tfcloud-operator. 1st way, undeploy the operator, delete the namespace and then recreate the namespace with the latest tfcloud-operator. 2nd way updates the tfcloud-operator in place.

#### Prerequisites

1. Install dependencies as instructed in [Tools needed](README.md) section.

1. Update cert-manager and traefik, this is managed by helm and following commands.

    ```sh
    make deploy-cert-manager
    make deploy-traefik
    ```

#### 1st way of updating tfcloud-operator from 2.1 to 2.2

1. Backup your custom resources in the namespace, e.g. database secrets

    ```sh
    kubectl -n your-namespace get secret yoursecret -o yaml > secret.yaml
    ```

1. Backup TF resources from the namespace

    ```sh
    kubectl -n your-namespace get tf -o yaml > tf.yaml
    ```

1. Deploy etcd operator

    ```sh
    make deploy-etcd
    ```

1. Delete the old namespace. **NOTICE** this will also remove the namespace, so save secretes/etc! Remeber also to remove any Tribefire deployments before undeploying the operator otherwise namespace deletion will get stuck. Ignore etcd errors this will produce, make sure that the namespace was deleted.

    1. Check tribefire deployments

        ```sh
        kubectl -n your-namespace get tf

        NAME           STATUS      AGE   DOMAIN   DATABASE   BACKEND   UNAVAILABLE
        phoenix-test   available   17h            local
        ```

    1. Delete tribefire deployments

        ```sh
        kubectl -n your-namespace delete tf phoenix-test
        ```

    1. Wait till the tribefire deployment is deleted

        ```sh
        watch kubectl -n your-namespace get po
        ```

    1. Delete namespace

        ```sh
        OPERATOR_NAMESPACE="your-namespace" make undeploy
        ```

1. Create the namespace

    ```sh
    OPERATOR_DOCKER_HOST="your.docker.host" \
        OPERATOR_NAMESPACE="your-namespace" make deploy
    ```

    * One can define the different images more granurarly

        ```sh
        OPERATOR_DOCKER_HOST="your.docker.host" \
        TRIBEFIRE_POSTGRESQL_CHECKER_IMAGE="your.docker.host/tribefire-cloud/postgres-checker:1.1" \
        TRIBEFIRE_POSTGRESQL_IMAGE="bitnami/postgresql:17" \
        ETCD_OPERATOR_IMAGE="your.docker.host/tribefire-cloud/etcd-operator:20250312-3983c32" \
        OPERATOR_NAMESPACE="your-namespace" \
            make deploy
        ```

    * Make sure that etcd cluster is up

        ```sh
        kubectl -n your-namespace get pods
        ```

1. Restore backup

    ```sh
    kubectl apply -f tf.yaml -f secret.yaml
    ```

1. Check TF status

    ```sh
    kubectl -n your-namespace get po
    ```

#### 2nd way of updating tfcloud-operator from 2.1 to 2.2

1. Update the tfcloud-operator. The straightforward way to do it is to edit the tfcloud-operator's config map in the particular namespace and add the following variables.

    1. Check configmap name

        ```sh
        kubectl -n your-namespace get cm

        NAME                                                    DATA   AGE
        kube-root-ca.crt                                        1      138d
        tfcloud-your-namespace-operator-config-map-t4b87979ck   15     138d
        ```

    2. Edit configmap

        ```sh
        kubectl -n your-namespace edit cm tfcloud-your-namespace-operator-config-map-t4b87979ck
        ```

    3. Add these variables
        * Remember to update the example values:

            `your.docker.host`

        ```sh
        TRIBEFIRE_POSTGRESQL_CHECKER_IMAGE: your.docker.host/tribefire-cloud/postgres-checker:1.1
        TRIBEFIRE_POSTGRESQL_IMAGE: ddocker340/postgres:16.8-alpine3.21-20250314
        OPERATOR_VERSION: v2.2
        ```

1. Update the operator deployment's image to the latest version, i.e.

    1. Check deployment name

        ```sh
        kubectl -n your-namespace get deployments.apps

        NAME                                        READY   UP-TO-DATE   AVAILABLE   AGE
        tfcloud-your-namespace-controller-manager   1/1     1            1           138d
        ```

    1. Update image

        ```sh
        kubectl -n your-namespace set image deployment/tfcloud-your-namespace-controller-manager manager=your.docker.host/tribefire-cloud/tribefire-operator:2.2
        ```

1. Update the etcd-operator. Easiest way to do this is to undeploy the existing deployment and then recreating it. After this you will need to recreate all etcd clusters. This step assumes that there is a valid file `config/manager/secrets/.dockerconfigjson` which will be used to pull the etcd-operator image.

    1. Undeploy

        ```sh
        make undeploy-etcd
        ```

    1. Recreate/deploy

        ```sh
        make deploy-etcd
        ```

1. Update etcd clusters. To do this you will need to recreate the etcd cluster and then restart the tribefire-master pod.

    1. Check etcd-cluster name

        ```sh
        kubectl -n your-namespace get etcdcluster
        ```

    1. Example template `new-etcd.yaml` for the new cluster:

        * Update values:

            `"your-etcd-cluster"`

            `your-namespace`

            ```sh
            apiVersion: "etcd.database.coreos.com/v1beta2"
            kind: "EtcdCluster"
            metadata:
            name: "your-etcd-cluster"
            namespace: your-namespace
            annotations:
                etcd.database.coreos.com/scope: clusterwide
            spec:
            size: 3
            pod:
                etcdEnv:
                - name: ETCD_AUTO_COMPACTION_RETENTION
                    value: "24"
                - name: ETCD_DEBUG
                    value: "false"
                - name: ETCD_HEARTBEAT_INTERVAL
                    value: "200"
            version: "v3.5.17-amd64"
            repository: gcr.io/etcd-development/etcd
            ```

    1. Delete the old cluster

        ```sh
        kubectl -n your-namespace delete etcdclusters.etcd.database.coreos.com your-etcd-cluster
        ```

    1. Then recreate it using the updated manifest

        ```sh
        kubectl -n your-namespace apply -f new-etcd.yaml
        ```

    1. After all 3 etcd pods are up restart tribefire master deployment

        ```sh
        watch kubectl -n your-namespace get po

        kubectl -n your-namespace rollout restart deployment tribefire-master-deployment
        ```

## Migration from 2.2 to 2.3

### Operator version 2.3 changes

* service accounts for the operator and for the Tribefire deployment are created with `automountServiceAccountToken: false`
* pods belonging to a Tribefire deployment are created with `readOnlyRootFilesystem: true`
  * operator will create the database pod with EmptyDir volumes that match the layout used by [Bitnami Postgresql](https://hub.docker.com/r/bitnami/postgresql)
  * reverting to old behavior (root volume is writable) is possible by setting an environment variable `TRIBEFIRE_POSTGRESQL_RO_ROOT` to `"false"` in the operator's config map, this is not recommended and can be useful in case of using a custom Postgresql image that does not follow the filesystem layout of the Bitnami image
* Application builds require Jinni 2.1.739.

### Migration procedure (2.2 -> 2.3)

One can either undeploy the operator, delete the namespace and then recreate the namespace with the latest tfcloud-operator - see the steps described in the migration procedure from 2.1 to 2.2. Or follow the instructions below.

1. Update the operator's `ServiceAccount` to include `automountServiceAccountToken: false`.

    1. Check service accounts

        ```sh
        kubectl -n adx get sa
        NAME                             SECRETS   AGE
        default                          0         17d
        phoenix-test                     0         17h
        tfcloud-adx-controller-manager   0         17d
        ```

    1. Edit operator's service account

        ```sh
        kubectl -n adx edit sa tfcloud-adx-controller-manager
        ```

        ```sh
        apiVersion: v1
        kind: ServiceAccount
        metadata:
          annotations:
            kubectl.kubernetes.io/last-applied-configuration: |
              {"apiVersion":"v1","kind":"ServiceAccount","metadata":{"annotations":{},"labels":{"app.kubernetes.io/component":"rbac","app.kubernetes.io/created-by":"tribefire-cloud","app.kubernetes.io/instance":"controller-manager-sa","app.kubernetes.io/managed-by":"kustomize","app.kubernetes.io/name":"serviceaccount","app.kubernetes.io/part-of":"tribefire-cloud"},"name":"tfcloud-adx-controller-manager","namespace":"adx"}}
          creationTimestamp: "2025-03-07T14:26:13Z"
          labels:
            app.kubernetes.io/component: rbac
            app.kubernetes.io/created-by: tribefire-cloud
            app.kubernetes.io/instance: controller-manager-sa
            app.kubernetes.io/managed-by: kustomize
            app.kubernetes.io/name: serviceaccount
            app.kubernetes.io/part-of: tribefire-cloud
          name: tfcloud-adx-controller-manager
          namespace: adx
          resourceVersion: "22047765"
          uid: 22e1c365-8073-4f42-bca0-7e1781ed2757
        automountServiceAccountToken: false # Add this line
        ```

1. Update the operator deployment to use the image version 2.3 and mount the service account token manually as a RO volume.
1. Then recreate any `tribefireruntimes` in the namespace.
