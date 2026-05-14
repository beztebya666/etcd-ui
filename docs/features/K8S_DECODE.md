# K8s decode + edit

The `kv` service links `k8s.io/api` (the same Go types kube-apiserver
ships) so it can read **and write** any object kube-apiserver stores in
etcd as a real structured value instead of an opaque protobuf blob.

## What gets decoded

519 Kinds across 19 API groups, every versioned subgroup:

| Group                          | Notable Kinds                                                    |
| ------------------------------ | ---------------------------------------------------------------- |
| `core/v1`                      | Pod, Service, ConfigMap, Secret, Namespace, Node, Endpoints, …  |
| `apps/v1`                      | Deployment, StatefulSet, DaemonSet, ReplicaSet, ControllerRevision |
| `batch/v1`                     | Job, CronJob                                                     |
| `networking.k8s.io/v1`         | Ingress, NetworkPolicy, IngressClass                             |
| `rbac.authorization.k8s.io/v1` | Role, RoleBinding, ClusterRole, ClusterRoleBinding               |
| `storage.k8s.io/v1`            | StorageClass, VolumeAttachment, CSINode, CSIDriver               |
| `policy/v1`                    | PodDisruptionBudget                                              |
| `coordination.k8s.io/v1`       | Lease — also used to surface holder identity on the Maintenance page |
| `discovery.k8s.io/v1`          | EndpointSlice                                                    |
| `autoscaling/v1`, `v2`         | HorizontalPodAutoscaler                                          |
| `admissionregistration.k8s.io/v1` | ValidatingWebhookConfiguration, MutatingWebhookConfiguration  |
| `flowcontrol.apiserver.k8s.io/v1` | FlowSchema, PriorityLevelConfiguration                        |
| `scheduling.k8s.io/v1`         | PriorityClass                                                    |
| `certificates.k8s.io/v1`       | CertificateSigningRequest                                        |
| `node.k8s.io/v1`               | RuntimeClass                                                     |
| `events.k8s.io/v1`             | Event                                                            |
| `authentication.k8s.io/v1`     | TokenReview, etc.                                                |
| `authorization.k8s.io/v1`      | SubjectAccessReview, etc.                                        |
| `apiserverinternal/v1alpha1`   | StorageVersion                                                   |

Plus every `v1beta1` / `v1beta2` / `v1beta3` deprecated variant still
present in older snapshots and pre-1.27 clusters.

## How decode works

kube-apiserver stores values in one of two formats:

1. **K8s protobuf** — bytes start with `k8s\0`, then a
   `runtime.Unknown` wrapper:
   ```
   message Unknown {
     optional TypeMeta typeMeta = 1;  // { apiVersion, kind }
     optional bytes raw = 2;          // the actual encoded object
   }
   ```
   `kv` parses the wrapper, looks up the Kind in our scheme, instantiates
   the right Go struct (e.g. `corev1.Pod{}`), unmarshals via the type's
   gen'd `Unmarshal()` method, then re-emits the populated struct as
   indented JSON. The result is byte-for-byte equivalent to what `kubectl
   get pod -o json` would print.

2. **JSON** — bytes start with `{`. Used for CRDs without a Go type the
   apiserver could pre-compile. We just pretty-print.

The handler that does this lives in
[`internal/k8sdecode/k8sdecode.go`](../../internal/k8sdecode/k8sdecode.go).
It's called from `cmd/kv/preview.go` for every KV in a Range response
and for every PUT/DELETE in a Watch stream.

## How edit (write-back) works

`POST /api/clusters/{id}/put-k8s` accepts:

```json
{
  "key": "/registry/pods/default/foo",
  "format": "k8s-proto",
  "apiVersion": "v1",
  "kind": "Pod",
  "json": "{ ... edited JSON ... }",
  "baseRev": 1383520515
}
```

Server pipeline:

1. `json.NewDecoder(...).DisallowUnknownFields().Decode(obj)` —
   typed validation. A typo like `replcas` instead of `replicas` is
   caught here, before any etcd write.
2. `obj.Marshal()` — the type's gen'd protobuf marshaller produces the
   inner wire bytes.
3. Wrap in `runtime.Unknown` (tag 0x0a `TypeMeta`, tag 0x12 `raw`,
   prefixed with `k8s\0`).
4. `cli.Txn().If(ModRevision(key) == baseRev).Then(Put(key, bytes))` —
   CAS guard so concurrent kubectl/controller writes don't get
   silently overwritten. A 409 surfaces "concurrent write — reload".

For CRDs stored as JSON we skip the proto encode and just minify the
JSON before writing.

Codec round-trip is verified by
[`internal/k8sdecode/k8sdecode_test.go::TestEncodeFromJSON_RoundTrip`](../../internal/k8sdecode/k8sdecode_test.go)
— a Pod is encoded to bytes, decoded to JSON, the image field is edited
in the JSON, re-encoded to bytes, decoded again, and the edit is
asserted to be present in the final JSON while metadata is intact.

## Limits

- We don't link **every** out-of-tree CRD's proto schema (Prometheus
  Operator, Istio, Argo, Crossplane all have their own typed protos).
  These CRDs get the metadata-only preview chip + raw hex fallback,
  with a `Raw protobuf` toggle in the viewer. To edit them, switch
  to raw and patch the bytes manually — or use `kubectl edit` for now.
- Edits go through one CAS attempt; a 409 means the SPA shows
  "concurrent write" and asks you to reload. We don't auto-merge K8s
  objects (CRDT 3-way merge is only for plain values via `/put-cas` —
  K8s schemas wouldn't tolerate marker-bearing values).
- The decoder uses `DisallowUnknownFields`. If you add a field that the
  pinned `k8s.io/api` version doesn't know about (because your cluster
  is newer than the version we link), the edit will fail. Pin bump:
  edit `go.mod` and bump `k8s.io/api` to match your cluster's minor.
