package k8sproto

import "testing"

func TestDecode_Pod(t *testing.T) {
	// Hand-crafted minimal k8s-protobuf Pod:
	// k8s\0 + 0x0a 0x09 (TypeMeta len=9) {0x0a 0x02 "v1" 0x12 0x03 "Pod"}
	// + 0x12 0x1c (raw len=28) {0x0a 0x1a (ObjMeta len=26)
	//   0x0a 0x0c "web-frontend" 0x1a 0x0a "production"}
	v := []byte{
		'k', '8', 's', 0,
		0x0a, 0x09,
		0x0a, 0x02, 'v', '1',
		0x12, 0x03, 'P', 'o', 'd',
		0x12, 0x1c,
		0x0a, 0x1a,
		0x0a, 0x0c, 'a', 'b', 'm', '-', 'f', 'r', 'o', 'n', 't', 'e', 'n', 'd',
		0x1a, 0x0a, 'p', 'r', 'o', 'd', 'u', 'c', 't', 'i', 'o', 'n',
	}
	p := Decode(v)
	if p == nil {
		t.Fatal("expected non-nil preview")
	}
	if p.Format != "k8s-proto" {
		t.Errorf("format: %q", p.Format)
	}
	if p.Kind != "Pod" || p.APIVersion != "v1" {
		t.Errorf("kind/api: %q %q", p.Kind, p.APIVersion)
	}
	if p.Name != "web-frontend" || p.Namespace != "production" {
		t.Errorf("name/ns: %q %q", p.Name, p.Namespace)
	}
}

func TestDecode_NotK8s(t *testing.T) {
	if Decode([]byte("plain text")) != nil {
		t.Fatal("text should not decode")
	}
	if Decode([]byte{0xff, 0xfe, 0xfd}) != nil {
		t.Fatal("random binary should not decode")
	}
	if Decode([]byte{}) != nil {
		t.Fatal("empty should not decode")
	}
}

func TestDecode_JSON(t *testing.T) {
	v := []byte(`{"kind":"Service","apiVersion":"v1","metadata":{"name":"api","namespace":"default"}}`)
	p := Decode(v)
	if p == nil {
		t.Fatal("expected non-nil preview for JSON")
	}
	if p.Format != "json" || p.Kind != "Service" || p.Name != "api" || p.Namespace != "default" {
		t.Errorf("got %+v", p)
	}
}

func TestDecode_TruncatedK8sProto(t *testing.T) {
	// Magic + TypeMeta only, no raw — should still return kind/api.
	v := []byte{
		'k', '8', 's', 0,
		0x0a, 0x09,
		0x0a, 0x02, 'v', '1',
		0x12, 0x03, 'P', 'o', 'd',
	}
	p := Decode(v)
	if p == nil || p.Kind != "Pod" || p.APIVersion != "v1" {
		t.Fatalf("got %+v", p)
	}
	if p.Name != "" || p.Namespace != "" {
		t.Errorf("should not have name/ns: %+v", p)
	}
}
