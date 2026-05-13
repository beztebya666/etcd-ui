package main

import "testing"

func TestClassifyHolder(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"/registry/leases/kube-node-lease/worker-3", "kubelet · worker-3"},
		{"/registry/leases/kube-system/kube-scheduler", "kube-scheduler"},
		{"/registry/leases/kube-system/kube-controller-manager", "kube-controller-manager"},
		{"/registry/leases/cert-manager/cert-manager-controller", "cert-manager/cert-manager-controller"},
		{"/random/key", ""},
	}
	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			if got := classifyHolder(tc.key); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPickK8sLeaseKey(t *testing.T) {
	if got := pickK8sLeaseKey([]string{"/other", "/registry/leases/kube-system/x"}); got != "/registry/leases/kube-system/x" {
		t.Errorf("should prefer K8s lease key, got %q", got)
	}
	if got := pickK8sLeaseKey([]string{"/foo", "/bar"}); got != "/foo" {
		t.Errorf("fallback should be first key, got %q", got)
	}
}

func TestLooksLikeHolder(t *testing.T) {
	for _, s := range []string{
		"kube-scheduler-rcd_8e123c",
		"node-aa-1.internal.example",
		"system:serviceaccount:kube-system:default",
	} {
		if !looksLikeHolder([]byte(s)) {
			t.Errorf("%q should be accepted as holder", s)
		}
	}
	for _, s := range []string{
		"",
		"ab",
		string([]byte{0x01, 0x02, 'a', 'b'}),
		"123456",
	} {
		if looksLikeHolder([]byte(s)) {
			t.Errorf("%q should be rejected as holder", s)
		}
	}
}

func TestFindLeaseTime(t *testing.T) {
	// RFC3339 with Z.
	v := []byte("foo bar 2026-05-13T22:30:11Z trailing")
	if got := findLeaseTime(v, ""); got != "2026-05-13T22:30:11Z" {
		t.Errorf("got %q", got)
	}
	// With offset.
	v2 := []byte("renew 2026-12-01T08:15:00+03:00 acquire ...")
	if got := findLeaseTime(v2, ""); got != "2026-12-01T08:15:00+03:00" {
		t.Errorf("got %q", got)
	}
	// No timestamp.
	if got := findLeaseTime([]byte("nothing here"), ""); got != "" {
		t.Errorf("should return empty: %q", got)
	}
}
