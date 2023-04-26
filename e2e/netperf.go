// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package e2e

import (
	"errors"
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/extension-kit/extutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	acorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	ametav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"strconv"
	"strings"
	"time"
)

type netperf struct {
	minikube  *Minikube
	ServerPod metav1.Object
	ClientPod metav1.Object
	Service   metav1.Object
}

func (n *netperf) Deploy(name string) error {
	serverPodName := fmt.Sprintf("%s-server", name)
	pod, err := n.minikube.CreatePod(&acorev1.PodApplyConfiguration{
		TypeMetaApplyConfiguration: ametav1.TypeMetaApplyConfiguration{
			Kind:       extutil.Ptr("Pod"),
			APIVersion: extutil.Ptr("v1"),
		},
		ObjectMetaApplyConfiguration: &ametav1.ObjectMetaApplyConfiguration{
			Name:   &serverPodName,
			Labels: map[string]string{"app": serverPodName},
		},
		Spec: &acorev1.PodSpecApplyConfiguration{
			RestartPolicy: extutil.Ptr(corev1.RestartPolicyNever),
			Containers: []acorev1.ContainerApplyConfiguration{
				{
					Name:  extutil.Ptr("netserver"),
					Image: extutil.Ptr("networkstatic/netserver:latest"),
					Args:  []string{"-D"},
					Ports: []acorev1.ContainerPortApplyConfiguration{
						{
							Name:          extutil.Ptr("control"),
							ContainerPort: extutil.Ptr(int32(12865)),
						},
						{
							Name:          extutil.Ptr("data"),
							ContainerPort: extutil.Ptr(int32(5000)),
						},
					},
				},
			},
		},
		Status: nil,
	})
	if err != nil {
		return err
	}
	n.ServerPod = pod

	service, err := n.minikube.CreateService(&acorev1.ServiceApplyConfiguration{
		TypeMetaApplyConfiguration: ametav1.TypeMetaApplyConfiguration{
			Kind:       extutil.Ptr("Service"),
			APIVersion: extutil.Ptr("v1"),
		},
		ObjectMetaApplyConfiguration: &ametav1.ObjectMetaApplyConfiguration{
			Name:   &name,
			Labels: map[string]string{"app": name},
		},
		Spec: &acorev1.ServiceSpecApplyConfiguration{
			Selector: n.ServerPod.GetLabels(),
			Ports: []acorev1.ServicePortApplyConfiguration{
				{
					Name:     extutil.Ptr("control"),
					Port:     extutil.Ptr(int32(12865)),
					Protocol: extutil.Ptr(corev1.ProtocolTCP),
				},
				{
					Name:     extutil.Ptr("data"),
					Port:     extutil.Ptr(int32(5000)),
					Protocol: extutil.Ptr(corev1.ProtocolTCP),
				},
			},
		},
	})
	if err != nil {
		return err
	}
	n.Service = service

	clientPodName := fmt.Sprintf("%s-client", name)
	pod, err = n.minikube.CreatePod(&acorev1.PodApplyConfiguration{
		TypeMetaApplyConfiguration: ametav1.TypeMetaApplyConfiguration{
			Kind:       extutil.Ptr("Pod"),
			APIVersion: extutil.Ptr("v1"),
		},
		ObjectMetaApplyConfiguration: &ametav1.ObjectMetaApplyConfiguration{
			Name:   &clientPodName,
			Labels: map[string]string{"app": clientPodName},
		},
		Spec: &acorev1.PodSpecApplyConfiguration{
			RestartPolicy: extutil.Ptr(corev1.RestartPolicyNever),
			Containers: []acorev1.ContainerApplyConfiguration{
				{
					Name:    extutil.Ptr("netperf"),
					Image:   extutil.Ptr("networkstatic/netperf:latest"),
					Command: []string{"sleep", "infinity"},
				},
			},
		},
		Status: nil,
	})
	if err != nil {
		return err
	}
	n.ClientPod = pod

	return nil
}

func (n *netperf) Target() (*action_kit_api.Target, error) {
	return NewContainerTarget(n.minikube, n.ServerPod, "netserver")
}

func (n *netperf) MeasureLatency() (time.Duration, error) {
	service := fmt.Sprintf("%s.%s.svc.cluster.local", n.Service.GetName(), n.Service.GetNamespace())

	var out string
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		out, err = n.minikube.Exec(n.ClientPod, "netperf", "netperf", "-H", service, "-l2", "-tTCP_RR", "--", "-P5000", "-r", "1,1", "-o", "mean_latency")
		if err == nil {
			break
		} else {
			if !strings.Contains(out, "Cannot assign requested address") {
				return 0, fmt.Errorf("%s: %s", err, out)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err != nil {
		return 0, fmt.Errorf("%s: %s", err, out)
	}

	lines := strings.Split(out, "\n")
	if len(lines) < 3 {
		return 0, fmt.Errorf("unexpected output: %s", out)
	}

	latency, err := strconv.ParseFloat(strings.TrimSpace(lines[2]), 64)
	if err != nil {
		return 0, fmt.Errorf("unexpected output: %s", out)
	}
	duration := time.Duration(latency) * time.Microsecond
	return duration, nil
}

func (n *netperf) Delete() error {
	return errors.Join(
		n.minikube.DeletePod(n.ServerPod),
		n.minikube.DeletePod(n.ClientPod),
		n.minikube.DeleteService(n.Service),
	)

}
