package k8sdecode

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// roundTrip marshals obj into the same k8s\0 + runtime.Unknown format
// kube-apiserver writes to etcd. Lets us round-trip known objects to
// verify the decoder produces identical Go structs.
func roundTrip(t *testing.T, obj runtime.Object, gvk schema.GroupVersionKind) []byte {
	t.Helper()
	// Inner: marshal the object's proto. Every K8s gen'd type has a
	// MarshalTo / Marshal method via gogo.
	m, ok := obj.(interface{ Marshal() ([]byte, error) })
	if !ok {
		t.Fatalf("type %T missing Marshal()", obj)
	}
	inner, err := m.Marshal()
	if err != nil {
		t.Fatalf("inner marshal: %v", err)
	}

	// Outer: build runtime.Unknown manually via wire bytes. Field 1
	// (TypeMeta) tag 0x0a; field 2 (raw) tag 0x12. Both length-delimited.
	var tm bytes.Buffer
	writeStringField(&tm, 1, gvk.GroupVersion().String())
	writeStringField(&tm, 2, gvk.Kind)

	var out bytes.Buffer
	out.WriteString("k8s\x00")
	writeBytesField(&out, 1, tm.Bytes())
	writeBytesField(&out, 2, inner)
	return out.Bytes()
}

func TestDecode_Pod(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "frontend-7c8b5f6d4-x9k2p",
			Namespace: "production",
			Labels:    map[string]string{"app": "frontend", "tier": "web"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "nginx:1.27",
					Ports: []corev1.ContainerPort{{ContainerPort: 80, Protocol: corev1.ProtocolTCP}},
				},
			},
			NodeName: "worker-3",
		},
	}
	raw := roundTrip(t, pod, schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})

	d := Decode(raw)
	if d == nil {
		t.Fatal("expected non-nil decode")
	}
	if d.Format != "k8s-proto" {
		t.Errorf("format: %q", d.Format)
	}
	if d.Kind != "Pod" || d.APIVersion != "v1" {
		t.Errorf("kind/api: %q %q", d.Kind, d.APIVersion)
	}
	if d.Name != "frontend-7c8b5f6d4-x9k2p" || d.Namespace != "production" {
		t.Errorf("metadata: %q / %q", d.Namespace, d.Name)
	}
	if d.JSON == "" {
		t.Fatalf("empty JSON, decode error: %s", d.DecodeError)
	}
	// Verify the inner spec round-tripped intact.
	var back map[string]any
	if err := json.Unmarshal([]byte(d.JSON), &back); err != nil {
		t.Fatalf("re-parse JSON: %v", err)
	}
	if !strings.Contains(d.JSON, `"nginx:1.27"`) {
		t.Errorf("expected image in JSON: %s", d.JSON)
	}
	if !strings.Contains(d.JSON, `"worker-3"`) {
		t.Errorf("expected nodeName in JSON: %s", d.JSON)
	}
	if !strings.Contains(d.JSON, `"kind": "Pod"`) {
		t.Errorf("expected injected kind in JSON: %s", d.JSON)
	}
}

func TestDecode_Deployment(t *testing.T) {
	replicas := int32(3)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "api"}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "api", Image: "myrepo/api:v2"}},
				},
			},
		},
	}
	raw := roundTrip(t, dep, schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"})
	d := Decode(raw)
	if d == nil || d.Kind != "Deployment" || d.JSON == "" {
		t.Fatalf("decode failed: %+v", d)
	}
	if !strings.Contains(d.JSON, `"replicas": 3`) {
		t.Errorf("expected replicas=3: %s", d.JSON)
	}
}

func TestDecode_JSONCRD(t *testing.T) {
	v := []byte(`{"apiVersion":"argoproj.io/v1alpha1","kind":"Application","metadata":{"name":"demo-app","namespace":"argo-cd"},"spec":{"project":"default"}}`)
	d := Decode(v)
	if d == nil {
		t.Fatal("expected non-nil")
	}
	if d.Format != "json" {
		t.Errorf("format: %q", d.Format)
	}
	if d.Kind != "Application" || d.APIVersion != "argoproj.io/v1alpha1" {
		t.Errorf("kind/api: %q %q", d.Kind, d.APIVersion)
	}
	if !strings.Contains(d.JSON, `"project": "default"`) {
		t.Errorf("expected pretty-printed spec: %s", d.JSON)
	}
}

func TestDecode_UnknownKind(t *testing.T) {
	// Build a wrapper with Kind=Frobnicator which isn't in our scheme.
	var tm bytes.Buffer
	writeStringField(&tm, 1, "example.com/v1")
	writeStringField(&tm, 2, "Frobnicator")
	var out bytes.Buffer
	out.WriteString("k8s\x00")
	writeBytesField(&out, 1, tm.Bytes())
	writeBytesField(&out, 2, []byte{0x0a, 0x04, 'f', 'o', 'o', '\x00'}) // junk inner

	d := Decode(out.Bytes())
	if d == nil {
		t.Fatal("expected non-nil")
	}
	if d.Kind != "Frobnicator" {
		t.Errorf("kind: %q", d.Kind)
	}
	if d.JSON != "" {
		t.Errorf("should not have JSON for unknown kind: %s", d.JSON)
	}
	if !strings.Contains(d.DecodeError, "no Go type registered") {
		t.Errorf("expected helpful error: %s", d.DecodeError)
	}
}

func TestDecode_NotK8s(t *testing.T) {
	if d := Decode([]byte("plain text")); d != nil {
		t.Fatalf("text should not decode: %+v", d)
	}
	if d := Decode([]byte{0xff, 0xfe}); d != nil {
		t.Fatalf("random bytes should not decode: %+v", d)
	}
	if d := Decode(nil); d != nil {
		t.Fatal("nil should not decode")
	}
}

func TestEncodeFromJSON_RoundTrip(t *testing.T) {
	// Build a Pod, encode it via roundTrip helper, decode via Decode,
	// edit the JSON, re-encode via EncodeFromJSON, decode again — final
	// object must reflect the edit.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "edit-me", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "main", Image: "nginx:1.0"}},
			NodeName:   "node-a",
		},
	}
	bytesV1 := roundTrip(t, pod, schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})

	d1 := Decode(bytesV1)
	if d1 == nil || d1.JSON == "" {
		t.Fatalf("initial decode failed: %+v", d1)
	}

	// Edit: swap image to nginx:2.0 by replacing the substring in the
	// pretty JSON the user would see in the SPA.
	edited := strings.Replace(d1.JSON, "nginx:1.0", "nginx:2.0", 1)
	if edited == d1.JSON {
		t.Fatal("edit substitution had no effect — check setup")
	}

	out, err := EncodeFromJSON(d1.APIVersion, d1.Kind, edited)
	if err != nil {
		t.Fatalf("EncodeFromJSON: %v", err)
	}

	d2 := Decode(out)
	if d2 == nil || d2.JSON == "" {
		t.Fatalf("re-decode failed: %+v", d2)
	}
	if !strings.Contains(d2.JSON, "nginx:2.0") {
		t.Errorf("round-trip lost edit, got: %s", d2.JSON)
	}
	if strings.Contains(d2.JSON, "nginx:1.0") {
		t.Errorf("old value leaked through: %s", d2.JSON)
	}
	// Sanity: name + namespace preserved.
	if d2.Name != "edit-me" || d2.Namespace != "default" {
		t.Errorf("metadata lost: %q / %q", d2.Namespace, d2.Name)
	}
}

func TestEncodeFromJSON_BadKind(t *testing.T) {
	_, err := EncodeFromJSON("example.com/v1", "Frobnicator", `{"metadata":{"name":"x"}}`)
	if err == nil {
		t.Fatal("expected error for unknown Kind")
	}
}

func TestEncodeFromJSON_BadJSON(t *testing.T) {
	_, err := EncodeFromJSON("v1", "Pod", `{this is not json}`)
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}

func TestSchemeCoverage(t *testing.T) {
	cov := DescribeSchemeCoverage()
	t.Logf("scheme coverage: %s", cov)
	// Sanity: we registered ~20 groups with hundreds of Kinds. Lower bound.
	if !strings.Contains(cov, "Kinds across") {
		t.Errorf("unexpected coverage format: %s", cov)
	}
}
