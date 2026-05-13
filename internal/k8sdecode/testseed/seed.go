// Standalone script: builds a fully-populated K8s Pod, encodes it the way
// kube-apiserver writes to etcd (k8s\0 + runtime.Unknown), and PUTs it
// at /registry/pods/shop/<name>. Used to seed the integration-test etcd
// so we can verify the SPA's K8s decoding pipeline against realistic
// payloads.
//
// Run: go run ./internal/k8sdecode/testseed http://127.0.0.1:12379

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: seed <etcd-endpoint>")
		os.Exit(2)
	}
	endpoint := os.Args[1]

	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-api-7c8b5f6d4-x9k2p",
			Namespace: "shop",
			Labels: map[string]string{
				"app":                          "orders-api",
				"tier":                         "backend",
				"app.kubernetes.io/managed-by": "argo-cd",
			},
			Annotations: map[string]string{
				"deployment.kubernetes.io/revision": "42",
			},
			CreationTimestamp: metav1.Now(),
		},
		Spec: corev1.PodSpec{
			NodeName: "worker-3.internal.example",
			Containers: []corev1.Container{
				{
					Name:  "main",
					Image: "registry.example.com/shop/accounts-api:v3.14.2",
					Ports: []corev1.ContainerPort{
						{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
						{Name: "metrics", ContainerPort: 9090, Protocol: corev1.ProtocolTCP},
					},
					Env: []corev1.EnvVar{
						{Name: "DATABASE_URL", Value: "postgres://demo-db:5432/accounts"},
						{Name: "LOG_LEVEL", Value: "info"},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("250m"),
							corev1.ResourceMemory: resource.MustParse("256Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("1000m"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.244.5.27",
			HostIP: "192.168.1.50",
		},
	}

	inner, err := pod.Marshal()
	if err != nil {
		panic(err)
	}

	// Build runtime.Unknown wrapper manually.
	tm := append([]byte{0x0a, 0x02}, "v1"...)
	tm = append(tm, 0x12, 0x03)
	tm = append(tm, "Pod"...)

	out := []byte("k8s\x00")
	out = append(out, 0x0a, byte(len(tm)))
	out = append(out, tm...)
	out = append(out, 0x12)
	n := uint64(len(inner))
	for n >= 0x80 {
		out = append(out, byte(n)|0x80)
		n >>= 7
	}
	out = append(out, byte(n))
	out = append(out, inner...)

	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{endpoint},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = cli.Put(ctx, "/registry/pods/shop/orders-api-7c8b5f6d4-x9k2p", string(out))
	if err != nil {
		panic(err)
	}

	fmt.Printf("ok, wrote %d bytes (%d inner proto) to /registry/pods/shop/orders-api-7c8b5f6d4-x9k2p\n", len(out), len(inner))
}
