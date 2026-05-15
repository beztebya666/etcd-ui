// Seeds the integration etcd with a realistic spread of K8s objects so
// the SPA's tree / decode / edit / watch pages have something to show.
// Run from CI or locally: `go run ./internal/k8sdecode/testseed <endpoint>`.

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	clientv3 "go.etcd.io/etcd/client/v3"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: seed <etcd-endpoint>")
		os.Exit(2)
	}
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{os.Args[1]},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	seedAll(ctx, cli)
}

func seedAll(ctx context.Context, cli *clientv3.Client) {
	replicas := int32(3)
	pods := []*corev1.Pod{
		makePod("shop", "orders-api-7c8b5f6d4-x9k2p",
			"registry.example.com/shop/orders-api:v3.14.2", "node-3.internal.example", "10.244.5.27"),
		makePod("shop", "catalog-api-65d9c-zptkj",
			"registry.example.com/shop/catalog-api:v2.8.1", "node-1.internal.example", "10.244.1.12"),
		makePod("shop", "checkout-api-78f4c-mvxqq",
			"registry.example.com/shop/checkout-api:v4.0.3", "node-2.internal.example", "10.244.2.45"),
		makePod("kube-system", "kube-scheduler-master-1",
			"registry.k8s.io/kube-scheduler:v1.30.4", "master-1", "10.244.0.5"),
		makePod("kube-system", "coredns-7db6d8ff4d-jbpkk",
			"registry.k8s.io/coredns/coredns:v1.11.1", "node-1.internal.example", "10.244.1.3"),
		makePod("monitoring", "prometheus-server-0",
			"quay.io/prometheus/prometheus:v2.55.0", "node-2.internal.example", "10.244.2.99"),
	}
	for _, p := range pods {
		key := fmt.Sprintf("/registry/pods/%s/%s", p.Namespace, p.Name)
		writeObj(ctx, cli, key, p, schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"})
	}

	deployments := []*appsv1.Deployment{
		makeDeployment("shop", "orders-api", &replicas, "registry.example.com/shop/orders-api:v3.14.2"),
		makeDeployment("shop", "catalog-api", &replicas, "registry.example.com/shop/catalog-api:v2.8.1"),
		makeDeployment("monitoring", "prometheus-server", &replicas, "quay.io/prometheus/prometheus:v2.55.0"),
	}
	for _, d := range deployments {
		key := fmt.Sprintf("/registry/deployments/%s/%s", d.Namespace, d.Name)
		writeObj(ctx, cli, key, d, schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"})
	}

	services := []*corev1.Service{
		makeService("shop", "orders-api", 8080),
		makeService("shop", "catalog-api", 8080),
		makeService("monitoring", "prometheus", 9090),
		makeService("kube-system", "kube-dns", 53),
	}
	for _, s := range services {
		key := fmt.Sprintf("/registry/services/specs/%s/%s", s.Namespace, s.Name)
		writeObj(ctx, cli, key, s, schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"})
	}

	configmaps := []*corev1.ConfigMap{
		makeConfigMap("shop", "orders-config", map[string]string{
			"DATABASE_URL": "postgres://demo-db:5432/orders",
			"LOG_LEVEL":    "info",
			"FEATURE_X":    "enabled",
		}),
		makeConfigMap("kube-system", "kube-dns-config", map[string]string{
			"upstreamNameservers": "[\"1.1.1.1\", \"8.8.8.8\"]",
		}),
	}
	for _, c := range configmaps {
		key := fmt.Sprintf("/registry/configmaps/%s/%s", c.Namespace, c.Name)
		writeObj(ctx, cli, key, c, schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"})
	}

	jobs := []*batchv1.Job{
		makeJob("shop", "shop-backup-2026-05-13", "registry.example.com/shop/backup:v1.0.0"),
	}
	for _, j := range jobs {
		key := fmt.Sprintf("/registry/jobs/%s/%s", j.Namespace, j.Name)
		writeObj(ctx, cli, key, j, schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"})
	}

	fmt.Printf("seeded %d pods, %d deployments, %d services, %d configmaps, %d jobs\n",
		len(pods), len(deployments), len(services), len(configmaps), len(jobs))
}

func makePod(ns, name, image, node, ip string) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: v1.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				"app":                          name,
				"tier":                         "backend",
				"app.kubernetes.io/managed-by": "argo-cd",
			},
			Annotations: map[string]string{
				"deployment.kubernetes.io/revision": "42",
			},
			CreationTimestamp: v1.Now(),
		},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{
				Name:  "main",
				Image: image,
				Ports: []corev1.ContainerPort{
					{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
				},
				Env: []corev1.EnvVar{
					{Name: "LOG_LEVEL", Value: "info"},
				},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("250m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: ip, HostIP: "192.168.1.50"},
	}
}

func makeDeployment(ns, name string, replicas *int32, image string) *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: v1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"},
		ObjectMeta: v1.ObjectMeta{Name: name, Namespace: ns,
			Labels: map[string]string{"app": name}},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
			Selector: &v1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: v1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: name, Image: image}},
				},
			},
		},
	}
}

func makeService(ns, name string, port int32) *corev1.Service {
	return &corev1.Service{
		TypeMeta: v1.TypeMeta{Kind: "Service", APIVersion: "v1"},
		ObjectMeta: v1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports: []corev1.ServicePort{{
				Port:       port,
				TargetPort: intstr.FromInt(int(port)),
				Protocol:   corev1.ProtocolTCP,
			}},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
}

func makeConfigMap(ns, name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta:   v1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
		ObjectMeta: v1.ObjectMeta{Name: name, Namespace: ns},
		Data:       data,
	}
}

func makeJob(ns, name, image string) *batchv1.Job {
	return &batchv1.Job{
		TypeMeta:   v1.TypeMeta{Kind: "Job", APIVersion: "batch/v1"},
		ObjectMeta: v1.ObjectMeta{Name: name, Namespace: ns},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers:    []corev1.Container{{Name: "main", Image: image}},
				},
			},
		},
	}
}

func writeObj(ctx context.Context, cli *clientv3.Client, key string, obj runtime.Object, gvk schema.GroupVersionKind) {
	m, ok := obj.(interface{ Marshal() ([]byte, error) })
	if !ok {
		fmt.Fprintf(os.Stderr, "skip %s: no Marshal\n", key)
		return
	}
	inner, err := m.Marshal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skip %s: %v\n", key, err)
		return
	}
	tm := append([]byte{0x0a, byte(len(gvk.GroupVersion().String()))}, gvk.GroupVersion().String()...)
	tm = append(tm, 0x12, byte(len(gvk.Kind)))
	tm = append(tm, gvk.Kind...)
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

	if _, err := cli.Put(ctx, key, string(out)); err != nil {
		fmt.Fprintf(os.Stderr, "put %s: %v\n", key, err)
	}
}
