// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2023 Steadybit GmbH

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/go-resty/resty/v2"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/extension-kit/extconversion"
	"github.com/steadybit/extension-kit/extutil"
	"k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	aappsv1 "k8s.io/client-go/applyconfigurations/apps/v1"
	acorev1 "k8s.io/client-go/applyconfigurations/core/v1"
	ametav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Extension struct {
	client *resty.Client
	stop   func() error
}

func (e *Extension) DiscoverTargets(targetId string) ([]discovery_kit_api.Target, error) {
	discoveries, err := e.describeDiscoveries()
	if err != nil {
		return nil, fmt.Errorf("failed to get discovery descriptions: %w", err)
	}
	for _, discovery := range discoveries {
		if discovery.Id == targetId {
			return e.discoverTargets(discovery)
		}
	}
	return nil, fmt.Errorf("discovery not found: %s", targetId)
}

func (e *Extension) RunAction(actionId string, target action_kit_api.Target, config interface{}) *ActionExecution {
	actions, err := e.describeActions()
	if err != nil {
		return errorExecution(fmt.Errorf("failed to get action descriptions: %w", err))
	}
	for _, action := range actions {
		if action.Id == actionId {
			return e.execAction(action, target, config)
		}
	}
	return errorExecution(fmt.Errorf("action not found: %s", actionId))
}

func (e *Extension) listDiscoveries() (discovery_kit_api.DiscoveryList, error) {
	var list discovery_kit_api.DiscoveryList
	res, err := e.client.R().SetResult(&list).Get("/")
	if err != nil {
		return list, fmt.Errorf("failed to get discovery list: %w", err)
	}
	if !res.IsSuccess() {
		return list, fmt.Errorf("failed to get discovery list: %d", res.StatusCode())
	}
	return list, nil
}

func (e *Extension) describeDiscoveries() ([]discovery_kit_api.DiscoveryDescription, error) {
	list, err := e.listDiscoveries()
	if err != nil {
		return nil, fmt.Errorf("failed to get discovery descriptions: %w", err)
	}

	discoveries := make([]discovery_kit_api.DiscoveryDescription, 0, len(list.Discoveries))
	for _, discovery := range list.Discoveries {
		description, err := e.describeDiscovery(discovery)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to describe discovery")
		}
		discoveries = append(discoveries, description)
	}
	return discoveries, nil
}

func (e *Extension) describeDiscovery(endpoint discovery_kit_api.DescribingEndpointReference) (discovery_kit_api.DiscoveryDescription, error) {
	var description discovery_kit_api.DiscoveryDescription
	res, err := e.client.R().SetResult(&description).Execute(string(endpoint.Method), endpoint.Path)
	if err != nil {
		return description, fmt.Errorf("failed to get discovery description: %w", err)
	}
	if !res.IsSuccess() {
		return description, fmt.Errorf("failed to get discovery description: %d", res.StatusCode())
	}
	return description, nil
}

func (e *Extension) discoverTargets(discovery discovery_kit_api.DiscoveryDescription) ([]discovery_kit_api.Target, error) {
	var targets discovery_kit_api.DiscoveredTargets
	res, err := e.client.R().SetResult(&targets).Execute(string(discovery.Discover.Method), discovery.Discover.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to discover targets: %w", err)
	}
	if !res.IsSuccess() {
		return nil, fmt.Errorf("failed to discover targets: %d", res.StatusCode())
	}
	return targets.Targets, nil
}

func (e *Extension) listActions() (action_kit_api.ActionList, error) {
	var list action_kit_api.ActionList
	res, err := e.client.R().SetResult(&list).Get("/")
	if err != nil {
		return list, fmt.Errorf("failed to get action list: %w", err)
	}
	if !res.IsSuccess() {
		return list, fmt.Errorf("failed to get action list: %d", res.StatusCode())
	}
	return list, nil
}

func (e *Extension) describeActions() ([]action_kit_api.ActionDescription, error) {
	list, err := e.listActions()
	if err != nil {
		return nil, fmt.Errorf("failed to get action descriptions: %w", err)
	}

	actions := make([]action_kit_api.ActionDescription, 0, len(list.Actions))
	for _, action := range list.Actions {
		description, err := e.describeAction(action)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to describe action")
		}
		actions = append(actions, description)
	}
	return actions, nil
}

func (e *Extension) describeAction(action action_kit_api.DescribingEndpointReference) (action_kit_api.ActionDescription, error) {
	var description action_kit_api.ActionDescription
	res, err := e.client.R().SetResult(&description).Execute(string(action.Method), action.Path)
	if err != nil {
		return description, fmt.Errorf("failed to get action description: %w", err)
	}
	if !res.IsSuccess() {
		return description, fmt.Errorf("failed to get action description: %d", res.StatusCode())
	}
	return description, nil
}

type ActionExecution struct {
	ch     <-chan error
	cancel context.CancelFunc
}

func (a *ActionExecution) Wait() error {
	return <-a.ch
}

func (a *ActionExecution) Cancel() error {
	a.cancel()
	return nil
}

func errorExecution(err error) *ActionExecution {
	ch := make(chan error)
	ch <- err
	close(ch)
	return &ActionExecution{ch: ch}
}

func (e *Extension) execAction(action action_kit_api.ActionDescription, target action_kit_api.Target, config interface{}) *ActionExecution {
	executionId := uuid.New()

	state, duration, err := e.prepareAction(action, target, config, executionId)
	if err != nil {
		return errorExecution(err)
	}
	log.Info().Str("actionId", action.Id).
		Interface("config", config).
		Interface("state", state).
		Msg("Action prepared")

	state, err = e.startAction(action, executionId, state)
	if err != nil {
		if action.Stop != nil {
			_ = e.stopAction(action, executionId, state)
		}
		return errorExecution(err)
	}
	log.Info().Str("actionId", action.Id).
		Interface("state", state).
		Msg("Action started")

	ch := make(chan error)
	var ctx context.Context
	var cancel context.CancelFunc
	if duration > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), duration)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	go func() {
		defer cancel()

		var err error
		if action.Status != nil {
			state, err = e.actionStatus(ctx, action, executionId, state)
		} else {
			<-ctx.Done()
		}

		if action.Stop != nil {
			stopErr := e.stopAction(action, executionId, state)
			if stopErr != nil {
				err = errors.Join(err, stopErr)
			} else {
				log.Info().Str("actionId", action.Id).Msg("Action stopped")
			}
		} else {
			ch <- err
		}

		log.Info().Str("actionId", action.Id).Msg("Action ended")
		close(ch)
	}()

	return &ActionExecution{
		ch:     ch,
		cancel: cancel,
	}
}

func (e *Extension) prepareAction(action action_kit_api.ActionDescription, target action_kit_api.Target, config interface{}, executionId uuid.UUID) (action_kit_api.ActionState, time.Duration, error) {
	var duration time.Duration
	prepareBody := action_kit_api.PrepareActionRequestBody{
		ExecutionId: executionId,
		Target:      &target,
	}
	if err := extconversion.Convert(config, &prepareBody.Config); err != nil {
		return nil, duration, fmt.Errorf("failed to convert config: %w", err)
	}

	if action.TimeControl == action_kit_api.External {
		duration = time.Duration(prepareBody.Config["duration"].(float64) * float64(time.Millisecond))
	}

	var prepareResult action_kit_api.PrepareResult
	res, err := e.client.R().SetBody(prepareBody).SetResult(&prepareResult).Execute(string(action.Prepare.Method), action.Prepare.Path)
	if err != nil {
		return nil, duration, fmt.Errorf("failed to prepare action: %w", err)
	}
	logMessages(prepareResult.Messages)
	if !res.IsSuccess() {
		return nil, duration, fmt.Errorf("failed to prepare action: %d", res.StatusCode())
	}
	if prepareResult.Error != nil {
		return nil, duration, fmt.Errorf("action failed: %v", *prepareResult.Error)
	}

	return prepareResult.State, duration, nil
}

func (e *Extension) startAction(action action_kit_api.ActionDescription, executionId uuid.UUID, state action_kit_api.ActionState) (action_kit_api.ActionState, error) {
	startBody := action_kit_api.StartActionRequestBody{
		ExecutionId: executionId,
		State:       state,
	}
	var startResult action_kit_api.StartResult
	res, err := e.client.R().SetBody(startBody).Execute(string(action.Start.Method), action.Start.Path)
	if err != nil {
		return state, fmt.Errorf("failed to start action: %w", err)
	}
	logMessages(startResult.Messages)
	if !res.IsSuccess() {
		return state, fmt.Errorf("failed to start action: %d", res.StatusCode())
	}
	if startResult.State != nil {
		state = *startResult.State
	}
	return state, nil
}

func (e *Extension) actionStatus(ctx context.Context, action action_kit_api.ActionDescription, executionId uuid.UUID, state action_kit_api.ActionState) (action_kit_api.ActionState, error) {
	interval, err := time.ParseDuration(*action.Status.CallInterval)
	if err != nil {
		interval = 1 * time.Second
	}

	for {
		select {
		case <-ctx.Done():
			return state, nil
		case <-time.After(interval):
			statusBody := action_kit_api.ActionStatusRequestBody{
				ExecutionId: executionId,
				State:       state,
			}
			var statusResult action_kit_api.StatusResult
			res, err := e.client.R().SetBody(statusBody).SetResult(&statusResult).Execute(string(action.Status.Method), action.Status.Path)
			if err != nil {
				return state, fmt.Errorf("failed to get action status: %w", err)
			}
			logMessages(statusResult.Messages)
			if !res.IsSuccess() {
				return state, fmt.Errorf("failed to get action status: %d", res.StatusCode())
			}
			if statusResult.State != nil {
				state = *statusResult.State
			}
			if statusResult.Error != nil {
				return state, fmt.Errorf("action failed: %v", *statusResult.Error)
			}

			log.Info().Str("actionId", action.Id).Bool("completed", statusResult.Completed).Msg("Action status")
			if statusResult.Completed {
				return state, nil
			}
		}
	}
}

func (e *Extension) stopAction(action action_kit_api.ActionDescription, executionId uuid.UUID, state action_kit_api.ActionState) error {
	stopBody := action_kit_api.StopActionRequestBody{
		ExecutionId: executionId,
		State:       state,
	}
	var stopResult action_kit_api.StopResult
	res, err := e.client.R().SetBody(stopBody).SetResult(&stopResult).Execute(string(action.Stop.Method), action.Stop.Path)
	if err != nil {
		return fmt.Errorf("failed to stop action: %w", err)
	}
	logMessages(stopResult.Messages)
	if !res.IsSuccess() {
		return fmt.Errorf("failed to stop action: %d", res.StatusCode())
	}
	if stopResult.Error != nil {
		return fmt.Errorf("action failed: %v", *stopResult.Error)
	}
	return nil
}

func logMessages(messages *action_kit_api.Messages) {
	if messages == nil {
		return
	}
	for _, msg := range *messages {
		level := zerolog.NoLevel
		if msg.Level != nil {
			level, _ = zerolog.ParseLevel(string(*msg.Level))
		}
		if level == zerolog.NoLevel {
			level = zerolog.InfoLevel
		}

		event := log.WithLevel(level)
		if msg.Fields != nil {
			event.Fields(*msg.Fields)
		}
		if msg.Type != nil {
			event.Str("type", *msg.Type)
		}
		event.Msg(msg.Message)
	}
}

func startExtension(minikube *Minikube, image string) (*Extension, error) {
	ctx := context.Background()

	if err := minikube.LoadImage(image); err != nil {
		return nil, err
	}

	//FIXME use helm chart
	daemonSet, err := minikube.Client().AppsV1().DaemonSets("default").Apply(ctx, &aappsv1.DaemonSetApplyConfiguration{
		TypeMetaApplyConfiguration: ametav1.TypeMetaApplyConfiguration{
			Kind:       extutil.Ptr("DaemonSet"),
			APIVersion: extutil.Ptr("apps/v1"),
		},
		ObjectMetaApplyConfiguration: &ametav1.ObjectMetaApplyConfiguration{
			Name: extutil.Ptr("extension-container"),
		},
		Spec: &aappsv1.DaemonSetSpecApplyConfiguration{
			Selector: &ametav1.LabelSelectorApplyConfiguration{
				MatchLabels: map[string]string{
					"app": "extension-container",
				},
			},
			Template: &acorev1.PodTemplateSpecApplyConfiguration{
				ObjectMetaApplyConfiguration: &ametav1.ObjectMetaApplyConfiguration{
					Labels: map[string]string{
						"app": "extension-container",
					},
					Annotations: map[string]string{
						"container.apparmor.security.beta.kubernetes.io/extension-container": "unconfined",
					},
				},
				Spec: &acorev1.PodSpecApplyConfiguration{
					Containers: []acorev1.ContainerApplyConfiguration{
						{
							Name:            extutil.Ptr("extension-container"),
							Image:           &image,
							ImagePullPolicy: extutil.Ptr(corev1.PullNever),
							SecurityContext: &acorev1.SecurityContextApplyConfiguration{
								SeccompProfile: &acorev1.SeccompProfileApplyConfiguration{
									Type: extutil.Ptr(corev1.SeccompProfileTypeUnconfined),
								},
								Capabilities: &acorev1.CapabilitiesApplyConfiguration{
									Add: []corev1.Capability{"SYS_ADMIN", "SYS_RESOURCE", "SYS_PTRACE", "KILL", "NET_ADMIN", "DAC_OVERRIDE", "SETUID", "SETGID", "AUDIT_WRITE"},
								},
								ReadOnlyRootFilesystem: extutil.Ptr(true),
							},
							Env: []acorev1.EnvVarApplyConfiguration{
								{
									Name:  extutil.Ptr("STEADYBIT_LOG_LEVEL"),
									Value: extutil.Ptr("debug"),
								},
							},
							VolumeMounts: []acorev1.VolumeMountApplyConfiguration{
								{
									Name:      extutil.Ptr("cgroup-root"),
									MountPath: extutil.Ptr("/sys/fs/cgroup"),
								},
								{
									Name:      extutil.Ptr("runtime-socket"),
									MountPath: extutil.Ptr(minikube.Runtime().DefaultSocket()),
								},
								{
									Name:      extutil.Ptr("runtime-runc-root"),
									MountPath: extutil.Ptr(minikube.Runtime().DefaultRuncRoot()),
								},
								{
									Name:      extutil.Ptr("tmp"),
									MountPath: extutil.Ptr("/tmp"),
								},
							},
						},
					},
					HostPID:     extutil.Ptr(true),
					HostNetwork: extutil.Ptr(true), //FIXME
					SecurityContext: &acorev1.PodSecurityContextApplyConfiguration{
						RunAsUser:    extutil.Ptr(int64(10000)),
						RunAsGroup:   extutil.Ptr(int64(10000)),
						RunAsNonRoot: extutil.Ptr(true),
					},
					Volumes: []acorev1.VolumeApplyConfiguration{
						{
							Name: extutil.Ptr("cgroup-root"),
							VolumeSourceApplyConfiguration: acorev1.VolumeSourceApplyConfiguration{
								HostPath: &acorev1.HostPathVolumeSourceApplyConfiguration{
									Path: extutil.Ptr("/sys/fs/cgroup"),
									Type: extutil.Ptr(corev1.HostPathDirectory),
								},
							},
						},
						{
							Name: extutil.Ptr("runtime-socket"),
							VolumeSourceApplyConfiguration: acorev1.VolumeSourceApplyConfiguration{
								HostPath: &acorev1.HostPathVolumeSourceApplyConfiguration{
									Path: extutil.Ptr(minikube.Runtime().DefaultSocket()),
									Type: extutil.Ptr(corev1.HostPathSocket),
								},
							},
						},
						{
							Name: extutil.Ptr("runtime-runc-root"),
							VolumeSourceApplyConfiguration: acorev1.VolumeSourceApplyConfiguration{
								HostPath: &acorev1.HostPathVolumeSourceApplyConfiguration{
									Path: extutil.Ptr(minikube.Runtime().DefaultRuncRoot()),
									Type: extutil.Ptr(corev1.HostPathDirectory),
								},
							},
						},
						{
							Name: extutil.Ptr("tmp"),
							VolumeSourceApplyConfiguration: acorev1.VolumeSourceApplyConfiguration{
								EmptyDir: &acorev1.EmptyDirVolumeSourceApplyConfiguration{},
							},
						},
					},
				},
			},
		},
	}, metav1.ApplyOptions{FieldManager: "application/apply-patch"})

	if err != nil {
		return nil, fmt.Errorf("failed to create extension daemonset: %w", err)
	}

	stop := func() error {
		return minikube.Client().AppsV1().DaemonSets("default").Delete(ctx, "extension-container", metav1.DeleteOptions{})
	}

	waitForPods(minikube, daemonSet)

	var outb bytes.Buffer
	cmd := exec.Command("docker", "port", minikube.profile, "8080")
	cmd.Stdout = &outb
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to get the adress for the extension: %w", err)
	}
	address := strings.TrimSpace(outb.String())

	client := resty.New().SetBaseURL(fmt.Sprintf("http://%s", address))
	log.Info().Msgf("extension is available at %s", address)
	return &Extension{client: client, stop: stop}, nil
}

func waitForPods(minikube *Minikube, daemonSet *v1.DaemonSet) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}

		pods, err := minikube.Client().CoreV1().Pods(daemonSet.GetNamespace()).List(ctx, metav1.ListOptions{
			LabelSelector: labels.Set(daemonSet.Spec.Selector.MatchLabels).String(),
		})
		if err != nil {
			log.Warn().Err(err).Msg("failed to list pods for extension")
		}

		for _, pod := range pods.Items {
			if err := waitForPodPhase(minikube, pod.GetObjectMeta(), corev1.PodRunning, 10*time.Second); err != nil {
				log.Warn().Err(err).Msg("pod is not running")
			}
			go tailLog(minikube, pod.GetObjectMeta())
		}
		if len(pods.Items) > 0 {
			break
		}
	}
}

func tailLog(minikube *Minikube, pod metav1.Object) {
	reader, err := minikube.Client().CoreV1().
		Pods(pod.GetNamespace()).
		GetLogs(pod.GetName(), &corev1.PodLogOptions{
			Follow: true,
		}).Stream(context.Background())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to tail logs")
	}
	defer func() { _ = reader.Close() }()
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fmt.Printf("📦%s\n", scanner.Text())
	}
}

func createExtensionContainer() (string, error) {
	cmd := exec.Command("make", "container")
	cmd.Dir = ".."
	cmd.Stdout = &prefixWriter{prefix: "⚒️", w: os.Stdout}
	cmd.Stderr = &prefixWriter{prefix: "⚒️", w: os.Stdout}

	if err := cmd.Run(); err != nil {
		return "", err
	}
	return "docker.io/library/extension-container:latest", nil
}
