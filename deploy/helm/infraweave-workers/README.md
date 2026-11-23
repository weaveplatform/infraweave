# infraweave-workers

Runs infraweave private workers in a Kubernetes cluster. The chart installs
the `workers.infraweave.io` CRDs, a replicated `infraweave worker-controller`
Deployment, its Lease, service account, namespaced Roles and network
policies, and the WorkerPools of its `pools` value. Each WorkerPool's
capacity becomes registered worker slots that execute runs in Pods of the
watch namespace.

```sh
helm install workers oci://ghcr.io/weaveplatform/charts/infraweave-workers \
  --version <version> --namespace infraweave-workers -f values.yaml
```

A published chart's default `controller.image` is the
`ghcr.io/weaveplatform/infraweave-worker` image of the same release, pinned
by digest. Verify both before installing:

```sh
identity=https://github.com/weaveplatform/infraweave/.github/workflows/worker-artifacts.yml@refs/heads/main
issuer=https://token.actions.githubusercontent.com
cosign verify --certificate-identity "$identity" --certificate-oidc-issuer "$issuer" \
  ghcr.io/weaveplatform/charts/infraweave-workers@<chart digest>
cosign verify --certificate-identity "$identity" --certificate-oidc-issuer "$issuer" \
  ghcr.io/weaveplatform/infraweave-worker@<image digest>
```

Each release's `worker-artifacts.json` asset lists both digests, the
binaries' SHA-256 and the runner images the release pins.

## Values

`values.schema.json` refuses every key it does not name, and each pool's
`spec` is the WorkerPool spec exactly as the CRD defines it. The values
hold no secret: create these first, or the install fails naming what is
missing.

| Reference | Where | Keys |
|---|---|---|
| `managementCredentialsRef` | Each watch namespace | `idKey` and `secretKey`, the worker-pool-controller API key Managed pools need |
| A pool's `spec.credentialsRef` | The pool's namespace | `privateKey`; `token` too for Existing and Public pools |
| `trust.caSecretRef` | The release namespace | A PEM bundle of a private authority certifying an endpoint |
| `initializationPolicyRef` | The release namespace | A Rego initialization policy in a ConfigMap or Secret |

Minimal values:

```yaml
clusterID: prod-eu-1
watchNamespaces: [infraweave-workers]
managementCredentialsRef: {name: management, idKey: id, secretKey: secret}
pools:
  - name: build
    namespace: infraweave-workers
    spec:
      endpoint: https://infraweave.example.com
      account: <account ID>
      space: <space ID>
      mode: Managed
      name: Build
      capacity: 2
      credentialsRef: {name: build-credentials}
      pod:
        storageSize: 12Gi
        resources:
          requests: {cpu: 500m, memory: 1Gi, ephemeral-storage: 12Gi}
          limits: {cpu: "2", memory: 4Gi, ephemeral-storage: 12Gi}
```

## Several installations

A release names its objects after itself, so several releases share a
namespace. Each watch namespace belongs to one release: Helm refuses a
second release that watches it. Helm installs CRDs once per cluster and
never deletes them; install further releases with `--skip-crds` if you
prefer.

## Health, upgrades and uninstall

A controller replica is ready while the API server answers its Lease and,
on the leader, once it has read every watch namespace and reached every
pool's installation. An upgrade rolls one replica at a time, a
replacement ready before a replica goes, and the new leader resumes each
busy slot. `helm uninstall` first runs a Job that deletes the release's
WorkerPools and waits, up to `uninstall.timeoutSeconds`, while the
controller ends their runs and deregisters their workers. It refuses
while a watch namespace holds a WorkerPool another owner created. The
installation refuses to delete a Managed pool that stacks still name, so
move those stacks to another pool first. A timeout fails the uninstall,
naming each pool that remains and why, and leaves the release to retry.
