// Package k8sdecode unmarshals K8s objects stored in etcd by kube-apiserver
// into their real Go types so we can render them as proper structured JSON
// instead of a hex dump.
//
// kube-apiserver writes objects either as:
//
//   1. K8s protobuf: bytes start with `k8s\0`, then a runtime.Unknown
//      wrapper carrying TypeMeta + raw proto bytes. Used for every type
//      that has a Go struct registered in a kube-apiserver scheme — i.e.
//      every built-in resource (Pod, Service, Deployment, …) and a handful
//      of well-known CRDs that ship Go types (Prometheus, ArgoCD, etc).
//
//   2. Plain JSON: bytes start with `{`. Used for CRDs that the
//      apiserver doesn't have a compiled-in Go type for. The CRD's
//      `storage` API version field controls whether the apiserver
//      negotiates protobuf or JSON — for any CRD without a typed proto
//      shim, JSON is the only option.
//
// We import every k8s.io/api group into a single runtime.Scheme. The
// scheme's serializer picks the right factory by Kind, unmarshals into
// the real struct, and we then JSON-marshal the result for the SPA.
// Unknown Kinds (typically CRDs with custom proto layouts) fall back to
// a metadata-only preview — same as before.

package k8sdecode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	admissionregv1 "k8s.io/api/admissionregistration/v1"
	admissionregv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	apiserverinternalv1alpha1 "k8s.io/api/apiserverinternal/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	appsv1beta1 "k8s.io/api/apps/v1beta1"
	appsv1beta2 "k8s.io/api/apps/v1beta2"
	authenticationv1 "k8s.io/api/authentication/v1"
	authenticationv1beta1 "k8s.io/api/authentication/v1beta1"
	authorizationv1 "k8s.io/api/authorization/v1"
	authorizationv1beta1 "k8s.io/api/authorization/v1beta1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	autoscalingv2beta1 "k8s.io/api/autoscaling/v2beta1"
	autoscalingv2beta2 "k8s.io/api/autoscaling/v2beta2"
	batchv1 "k8s.io/api/batch/v1"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	certificatesv1 "k8s.io/api/certificates/v1"
	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
	coordinationv1 "k8s.io/api/coordination/v1"
	coordinationv1beta1 "k8s.io/api/coordination/v1beta1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	discoveryv1beta1 "k8s.io/api/discovery/v1beta1"
	eventsv1 "k8s.io/api/events/v1"
	eventsv1beta1 "k8s.io/api/events/v1beta1"
	flowcontrolv1 "k8s.io/api/flowcontrol/v1"
	flowcontrolv1beta1 "k8s.io/api/flowcontrol/v1beta1"
	flowcontrolv1beta2 "k8s.io/api/flowcontrol/v1beta2"
	flowcontrolv1beta3 "k8s.io/api/flowcontrol/v1beta3"
	networkingv1 "k8s.io/api/networking/v1"
	networkingv1beta1 "k8s.io/api/networking/v1beta1"
	nodev1 "k8s.io/api/node/v1"
	nodev1beta1 "k8s.io/api/node/v1beta1"
	policyv1 "k8s.io/api/policy/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	rbacv1beta1 "k8s.io/api/rbac/v1beta1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	storagev1beta1 "k8s.io/api/storage/v1beta1"
)

// Decoded is the structured output passed to the SPA. Only Kind/APIVersion
// are guaranteed populated; JSON is empty for unknown Kinds (caller falls
// back to metadata-only preview).
type Decoded struct {
	Format     string `json:"format"`     // "k8s-proto" | "json"
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
	// JSON-encoded full object (string, not raw bytes, so the SPA can
	// safely round-trip it). Empty when we couldn't fully decode.
	JSON string `json:"json,omitempty"`
	// DecodeError describes WHY we couldn't fully decode, when JSON is
	// empty — helps operators understand whether it's an unknown Kind,
	// schema drift, or a malformed payload. Never set together with JSON.
	DecodeError string `json:"decodeError,omitempty"`
}

var k8sMagic = []byte{'k', '8', 's', 0}

// scheme holds every K8s API group we link in. Initialised exactly once;
// concurrent reads after that are lock-free.
var (
	scheme     *runtime.Scheme
	schemeOnce sync.Once
)

func getScheme() *runtime.Scheme {
	schemeOnce.Do(func() {
		s := runtime.NewScheme()
		// Order doesn't matter — each AddToScheme is idempotent and only
		// registers the types in its own group/version. We list every
		// versioned group kube-apiserver might serve in any K8s release
		// since 1.20, including deprecated betas (still present in older
		// snapshots, etcd backups, etc).
		adders := []func(*runtime.Scheme) error{
			admissionregv1.AddToScheme,
			admissionregv1beta1.AddToScheme,
			apiserverinternalv1alpha1.AddToScheme,
			appsv1.AddToScheme,
			appsv1beta1.AddToScheme,
			appsv1beta2.AddToScheme,
			authenticationv1.AddToScheme,
			authenticationv1beta1.AddToScheme,
			authorizationv1.AddToScheme,
			authorizationv1beta1.AddToScheme,
			autoscalingv1.AddToScheme,
			autoscalingv2.AddToScheme,
			autoscalingv2beta1.AddToScheme,
			autoscalingv2beta2.AddToScheme,
			batchv1.AddToScheme,
			batchv1beta1.AddToScheme,
			certificatesv1.AddToScheme,
			certificatesv1beta1.AddToScheme,
			coordinationv1.AddToScheme,
			coordinationv1beta1.AddToScheme,
			corev1.AddToScheme,
			discoveryv1.AddToScheme,
			discoveryv1beta1.AddToScheme,
			eventsv1.AddToScheme,
			eventsv1beta1.AddToScheme,
			flowcontrolv1.AddToScheme,
			flowcontrolv1beta1.AddToScheme,
			flowcontrolv1beta2.AddToScheme,
			flowcontrolv1beta3.AddToScheme,
			networkingv1.AddToScheme,
			networkingv1beta1.AddToScheme,
			nodev1.AddToScheme,
			nodev1beta1.AddToScheme,
			policyv1.AddToScheme,
			policyv1beta1.AddToScheme,
			rbacv1.AddToScheme,
			rbacv1beta1.AddToScheme,
			schedulingv1.AddToScheme,
			storagev1.AddToScheme,
			storagev1beta1.AddToScheme,
		}
		for _, add := range adders {
			_ = add(s)
		}
		scheme = s
	})
	return scheme
}

// Decode inspects the raw etcd value and returns a structured Decoded.
// Returns nil if the input doesn't look like anything we recognise
// (caller should fall back to hex / text rendering).
func Decode(raw []byte) *Decoded {
	if len(raw) == 0 {
		return nil
	}
	// JSON-encoded CRD: read the kind/apiVersion/metadata from a partial
	// parse and pretty-print the rest.
	if raw[0] == '{' {
		return decodeJSON(raw)
	}
	if len(raw) > 4 && bytes.Equal(raw[:4], k8sMagic) {
		return decodeProto(raw[4:])
	}
	return nil
}

// decodeJSON pretty-formats a JSON K8s object and extracts the typical
// header fields. We don't validate the JSON is actually a K8s object —
// any well-formed JSON works; the caller decides whether to use the
// `kind` field for display.
func decodeJSON(raw []byte) *Decoded {
	var probe struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return &Decoded{Format: "json", DecodeError: "invalid JSON: " + err.Error()}
	}
	// Pretty-print the whole thing so the SPA can show colour-coded
	// structured view without doing JSON.parse client-side.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		// Should never happen given the probe parsed, but degrade nicely.
		pretty.Write(raw)
	}
	return &Decoded{
		Format:     "json",
		APIVersion: probe.APIVersion,
		Kind:       probe.Kind,
		Namespace:  probe.Metadata.Namespace,
		Name:       probe.Metadata.Name,
		JSON:       pretty.String(),
	}
}

// decodeProto unmarshals the runtime.Unknown wrapper, looks up the Kind
// in our scheme, instantiates the right Go struct, fills it, and emits
// the resulting object as JSON. Anything we can't do falls back to a
// metadata-only Decoded with a DecodeError describing the failure.
func decodeProto(v []byte) *Decoded {
	apiVersion, kind, raw, ok := readTypeMetaAndRaw(v)
	if !ok {
		return &Decoded{Format: "k8s-proto", DecodeError: "wrapper not parseable"}
	}
	d := &Decoded{Format: "k8s-proto", APIVersion: apiVersion, Kind: kind}

	// Always extract Namespace + Name from the inner ObjectMeta first — if
	// the full decode below fails we still want these populated. They live
	// at the same well-known offset regardless of Kind.
	d.Name, d.Namespace = readObjectMeta(raw)

	if apiVersion == "" || kind == "" {
		d.DecodeError = "missing TypeMeta in wrapper"
		return d
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		d.DecodeError = "bad apiVersion: " + err.Error()
		return d
	}
	gvk := gv.WithKind(kind)
	s := getScheme()
	obj, err := s.New(gvk)
	if err != nil {
		// Kind not registered in our scheme — typical for CRDs that
		// kube-apiserver served as protobuf via a typed shim we don't
		// have. Caller still gets metadata; the binary viewer stays.
		d.DecodeError = "no Go type registered for " + gvk.String()
		return d
	}

	// K8s protobuf uses gogo/protobuf code generation; the standard
	// google.golang.org/protobuf Unmarshal doesn't always recognise it.
	// We rely on the type's own Unmarshal method (every generated K8s
	// proto type implements proto.Unmarshaler).
	if u, ok := obj.(interface{ Unmarshal([]byte) error }); ok {
		if err := u.Unmarshal(raw); err != nil {
			d.DecodeError = "proto unmarshal: " + err.Error()
			return d
		}
	} else {
		d.DecodeError = "type missing Unmarshal method"
		return d
	}

	// Inject TypeMeta into the Go object — proto types don't carry it,
	// but K8s users expect it at the top of the JSON output (matches
	// `kubectl get -o yaml` behaviour).
	if tmSetter, ok := obj.(interface{ GetObjectKind() schema.ObjectKind }); ok {
		tmSetter.GetObjectKind().SetGroupVersionKind(gvk)
	}

	b, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		d.DecodeError = "json marshal: " + err.Error()
		return d
	}
	d.JSON = string(b)
	return d
}

// --- runtime.Unknown wire-format helpers ---------------------------------
//
// Inlined from internal/k8sproto to avoid the import cycle that would
// happen if that package also depended on this one (preview decoder is
// the entry point; this is the deeper decoder).

func readTypeMetaAndRaw(v []byte) (apiVersion, kind string, raw []byte, ok bool) {
	if len(v) == 0 || v[0] != 0x0a {
		return "", "", nil, false
	}
	tlen, n := readVarint(v[1:])
	if n == 0 {
		return "", "", nil, false
	}
	tmStart := 1 + n
	tmEnd := tmStart + int(tlen)
	if tmEnd > len(v) {
		return "", "", nil, false
	}
	apiVersion, kind = readTypeMeta(v[tmStart:tmEnd])
	rest := v[tmEnd:]
	if len(rest) >= 2 && rest[0] == 0x12 {
		rl, m := readVarint(rest[1:])
		if m > 0 {
			rStart := 1 + m
			rEnd := rStart + int(rl)
			if rEnd <= len(rest) {
				raw = rest[rStart:rEnd]
			}
		}
	}
	return apiVersion, kind, raw, true
}

func readTypeMeta(v []byte) (apiVersion, kind string) {
	for len(v) > 0 {
		tag := v[0]
		v = v[1:]
		if tag&0x07 != 2 {
			return
		}
		l, n := readVarint(v)
		if n == 0 || int(l) > len(v)-n {
			return
		}
		s := string(v[n : n+int(l)])
		v = v[n+int(l):]
		switch tag >> 3 {
		case 1:
			apiVersion = s
		case 2:
			kind = s
		}
	}
	return
}

func readObjectMeta(v []byte) (name, namespace string) {
	if len(v) < 2 || v[0] != 0x0a {
		return
	}
	mlen, n := readVarint(v[1:])
	if n == 0 {
		return
	}
	start := 1 + n
	end := start + int(mlen)
	if end > len(v) {
		return
	}
	body := v[start:end]
	for len(body) > 0 {
		tag := body[0]
		body = body[1:]
		if tag&0x07 != 2 {
			return
		}
		l, m := readVarint(body)
		if m == 0 || int(l) > len(body)-m {
			return
		}
		s := string(body[m : m+int(l)])
		body = body[m+int(l):]
		switch tag >> 3 {
		case 1:
			name = s
		case 3:
			namespace = s
		}
		if name != "" && namespace != "" {
			return
		}
	}
	return
}

func readVarint(v []byte) (uint64, int) {
	var x uint64
	var s uint
	for i, b := range v {
		if i >= 10 {
			return 0, 0
		}
		if b < 0x80 {
			return x | uint64(b)<<s, i + 1
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, 0
}

// Errors returned by the public API.
var (
	ErrNotK8s = errors.New("k8sdecode: not a K8s-encoded value")
)

// DescribeSchemeCoverage is a debug helper used by tests — returns a
// short summary of what's registered (counts per group).
func DescribeSchemeCoverage() string {
	s := getScheme()
	counts := map[string]int{}
	for gvk := range s.AllKnownTypes() {
		counts[gvk.Group]++
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d Kinds across %d groups", len(s.AllKnownTypes()), len(counts))
	return sb.String()
}
