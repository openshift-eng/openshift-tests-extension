// Package updateservice deploys a fake Cincinnati update service (a pod
// serving a hand-built update graph over HTTP) into a test namespace, so
// tests can point ClusterVersion.spec.upstream at it and have the CVO
// reconcile arbitrary graph contents into status.availableUpdates and
// status.conditionalUpdates.
//
// The package is deliberately framework-agnostic and dependency-restricted
// so that external openshift-tests-extension (OTE) suites can import it
// without dragging in origin's test framework: it must only use the standard
// library and the k8s.io/{api,apimachinery,client-go} modules, and must not
// import ginkgo/gomega, origin's exutil, or anything that transitively
// reaches k8s.io/kubernetes.
package updateservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// DefaultShellImage is a pull spec any pod on an OpenShift cluster can pull
// and that contains python3, which the update service serves the graph with.
const DefaultShellImage = "image-registry.openshift-image-registry.svc:5000/openshift/tools:latest"

const (
	deploymentLabel = "test-update-service"
	containerName   = "update-service"
	containerPort   = 8000
	graphPath       = "graph"

	pollInterval = 2 * time.Second
	readyTimeout = 200 * time.Second
)

// UpdateService is a fake Cincinnati update service deployed by Run.
type UpdateService struct {
	// Name is the shared name of the backing Deployment and Service.
	Name string
	// URL is the in-cluster URL of the graph endpoint, suitable as a
	// ClusterVersion spec.upstream value.
	URL *url.URL

	client    kubernetes.Interface
	namespace string
}

// Run creates a Deployment and Service serving graph in namespace and waits
// for the deployment to become ready. shellImage must contain python3; pass
// "" to use DefaultShellImage. Cleanup on the returned UpdateService removes
// the created resources; deleting the namespace also works.
func Run(ctx context.Context, client kubernetes.Interface, namespace, shellImage string, graph Graph) (*UpdateService, error) {
	if shellImage == "" {
		shellImage = DefaultShellImage
	}
	payload, err := json.Marshal(graph)
	if err != nil {
		return nil, fmt.Errorf("marshaling update graph: %w", err)
	}

	deployment, err := client.AppsV1().Deployments(namespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-update-service-",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": deploymentLabel,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": deploymentLabel,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  containerName,
						Image: shellImage,
						Args: []string{
							"/bin/sh",
							"-c",
							fmt.Sprintf(`DIR="$(mktemp -d)" &&
cd "${DIR}" &&
printf '%%s' '%s' >graph &&
python3 -m http.server --bind ::
`, payload),
						},
						Ports: []corev1.ContainerPort{{
							Name:          containerName,
							ContainerPort: containerPort,
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("10m"),
								corev1.ResourceMemory: resource.MustParse("20Mi"),
							},
						},
					}},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating update-service deployment: %w", err)
	}

	service, err := client.CoreV1().Services(namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: deployment.Name,
		},
		Spec: corev1.ServiceSpec{
			Selector: deployment.Spec.Template.Labels,
			Ports: []corev1.ServicePort{{
				Name: containerName,
				Port: containerPort,
			}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating update-service service: %w", err)
	}

	if err := waitForDeploymentReady(ctx, client, namespace, deployment.Name); err != nil {
		return nil, err
	}

	return &UpdateService{
		Name: deployment.Name,
		URL: &url.URL{
			Scheme: "http",
			Host:   net.JoinHostPort(service.Spec.ClusterIP, strconv.Itoa(int(service.Spec.Ports[0].Port))),
			Path:   graphPath,
		},
		client:    client,
		namespace: namespace,
	}, nil
}

// Cleanup deletes the Deployment and Service created by Run.
func (s *UpdateService) Cleanup(ctx context.Context) error {
	return errors.Join(
		s.client.AppsV1().Deployments(s.namespace).Delete(ctx, s.Name, metav1.DeleteOptions{}),
		s.client.CoreV1().Services(s.namespace).Delete(ctx, s.Name, metav1.DeleteOptions{}),
	)
}

func waitForDeploymentReady(ctx context.Context, client kubernetes.Interface, namespace, name string) error {
	var lastErr error
	err := wait.PollUntilContextTimeout(ctx, pollInterval, readyTimeout, true, func(ctx context.Context) (bool, error) {
		deployment, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			// keep polling through transient errors until the timeout
			lastErr = err
			return false, nil
		}
		replicas := int32(1)
		if deployment.Spec.Replicas != nil {
			replicas = *deployment.Spec.Replicas
		}
		return deployment.Status.AvailableReplicas == replicas, nil
	})
	if err != nil {
		if lastErr != nil {
			err = fmt.Errorf("%w; last error: %v", err, lastErr)
		}
		return fmt.Errorf("waiting for update-service deployment %s/%s to become ready: %w", namespace, name, err)
	}
	return nil
}
