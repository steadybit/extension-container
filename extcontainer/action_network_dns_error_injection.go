// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2026 Steadybit GmbH

package extcontainer

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_commons/network"
	"github.com/steadybit/action-kit/go/action_kit_commons/network/dnsinject"
	"github.com/steadybit/action-kit/go/action_kit_commons/ociruntime"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-container/config"
	"github.com/steadybit/extension-container/extcontainer/container/types"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
)

var _ action_kit_sdk.Action[DNSErrorInjectionState] = (*dnsErrorInjectionAction)(nil)
var _ action_kit_sdk.ActionWithStatus[DNSErrorInjectionState] = (*dnsErrorInjectionAction)(nil)
var _ action_kit_sdk.ActionWithStop[DNSErrorInjectionState] = (*dnsErrorInjectionAction)(nil)

var (
	dnsInjectHandles     = map[string]dnsinject.DNSInject{}
	dnsInjectHandlesLock sync.Mutex
)

type DNSErrorInjectionState struct {
	ExecutionId string
	ContainerID string
}

type dnsErrorInjectionAction struct {
	ociRuntime ociruntime.OciRuntime
	client     types.Client
}

func NewNetworkDNSErrorInjectionAction(r ociruntime.OciRuntime, client types.Client) action_kit_sdk.Action[DNSErrorInjectionState] {
	return &dnsErrorInjectionAction{ociRuntime: r, client: client}
}

func (a *dnsErrorInjectionAction) NewEmptyState() DNSErrorInjectionState {
	return DNSErrorInjectionState{}
}

func (a *dnsErrorInjectionAction) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          fmt.Sprintf("%s.network_dns_error_injection", BaseActionID),
		Label:       "DNS Error Injection",
		Description: "Inject DNS errors (NXDOMAIN/SERVFAIL/TIMEOUT) into DNS queries using eBPF.",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(dnsIcon),
		TargetSelection: &action_kit_api.TargetSelection{
			TargetType:         targetID,
			SelectionTemplates: &targetSelectionTemplates,
		},
		Technology:  extutil.Ptr("Container"),
		Category:    extutil.Ptr("Network"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Status: extutil.Ptr(action_kit_api.MutatingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr("2s"),
		}),
		Widgets: extutil.Ptr([]action_kit_api.Widget{
			action_kit_api.MarkdownWidget{
				Type:        action_kit_api.ComSteadybitWidgetMarkdown,
				Title:       "DNS Error Injection Statistics",
				MessageType: "dns_stats_markdown",
				Append:      false,
			},
		}),
		Parameters : dnsErrorInjectionParameters(),
	}
}

func (a *dnsErrorInjectionAction) Prepare(ctx context.Context, state *DNSErrorInjectionState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	container, _, err := getContainerTarget(ctx, a.client, *request.Target)
	if err != nil {
		return nil, err
	}
	containerId := RemovePrefix(container.Id())

	processInfo, err := getProcessInfoForContainer(ctx, a.ociRuntime, containerId, specs.NetworkNamespace)
	if err != nil {
		return nil, err
	}

	if isUsingHostNetwork(processInfo.Namespaces) {
		if config.Config.DisallowHostNetwork {
			return &action_kit_api.PrepareResult{
				Error: &action_kit_api.ActionKitError{
					Title:  "Container is using host network. This is disallowed by your system administrators.",
					Status: extutil.Ptr(action_kit_api.Failed),
				},
			}, nil
		}
		if extutil.ToBool(request.Config["failOnHostNetwork"]) {
			return &action_kit_api.PrepareResult{
				Error: &action_kit_api.ActionKitError{
					Title:  "Container is using host network and failOnHostNetwork = true.",
					Status: extutil.Ptr(action_kit_api.Failed),
				},
			}, nil
		}
	}

	opts, err := parseDNSInjectOpts(request.Config)
	if err != nil {
		return nil, err
	}

	sidecarId := fmt.Sprintf("%s-%s", request.ExecutionId.String()[24:], containerId[:min(8, len(containerId))])
	handle, err := dnsinject.NewProcess(ctx, a.ociRuntime, processInfo, sidecarId, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create dns-inject process: %w", err)
	}

	state.ExecutionId = request.ExecutionId.String()
	state.ContainerID = containerId

	dnsInjectHandlesLock.Lock()
	dnsInjectHandles[state.ExecutionId] = handle
	dnsInjectHandlesLock.Unlock()

	return &action_kit_api.PrepareResult{}, nil
}

func (a *dnsErrorInjectionAction) Start(_ context.Context, state *DNSErrorInjectionState) (*action_kit_api.StartResult, error) {
	handle, ok := getDNSInjectHandle(state.ExecutionId)
	if !ok {
		return nil, fmt.Errorf("no dns-inject handle found for execution %s", state.ExecutionId)
	}

	if err := handle.Start(); err != nil {
		return nil, fmt.Errorf("failed to start dns-inject: %w", err)
	}

	return &action_kit_api.StartResult{}, nil
}

func (a *dnsErrorInjectionAction) Status(_ context.Context, state *DNSErrorInjectionState) (*action_kit_api.StatusResult, error) {
	handle, ok := getDNSInjectHandle(state.ExecutionId)
	if !ok {
		return &action_kit_api.StatusResult{Completed: true}, nil
	}

	if exited, err := handle.Exited(); exited {
		removeDNSInjectHandle(state.ExecutionId)
		errMsg := "dns-inject exited unexpectedly"
		if err != nil {
			errMsg = fmt.Sprintf("dns-inject failed: %v", err)
		}
		return &action_kit_api.StatusResult{
			Completed: true,
			Error: &action_kit_api.ActionKitError{
				Title:  errMsg,
				Status: extutil.Ptr(action_kit_api.Errored),
			},
		}, nil
	}

	metrics, err := handle.Metrics()
	if err != nil {
		return &action_kit_api.StatusResult{Completed: false}, nil
	}

	return &action_kit_api.StatusResult{
		Completed: false,
		Messages:  extutil.Ptr(formatDNSMetricsMessages(metrics)),
	}, nil
}

func (a *dnsErrorInjectionAction) Stop(_ context.Context, state *DNSErrorInjectionState) (*action_kit_api.StopResult, error) {
	handle, ok := getDNSInjectHandle(state.ExecutionId)
	if !ok {
		return nil, nil
	}
	removeDNSInjectHandle(state.ExecutionId)

	if err := handle.Stop(); err != nil {
		log.Warn().Err(err).Str("execution_id", state.ExecutionId).Msg("failed to stop dns-inject")
	}

	return nil, nil
}

// helpers

func dnsErrorInjectionParameters() []action_kit_api.ActionParameter {
	return []action_kit_api.ActionParameter{
		{
			Name:         "duration",
			Label:        "Duration",
			Description:  extutil.Ptr("How long should the DNS errors be injected?"),
			Type:         action_kit_api.ActionParameterTypeDuration,
			DefaultValue: extutil.Ptr("30s"),
			Required:     extutil.Ptr(true),
			Order:        extutil.Ptr(0),
		},
		{
			Name:         "dnsErrorType",
			Label:        "DNS Error Type",
			Description:  extutil.Ptr("Which DNS errors to inject? Multiple types can be selected for random injection."),
			Type:         action_kit_api.ActionParameterTypeStringArray,
			DefaultValue: extutil.Ptr("[\"NXDOMAIN\"]"),
			Required:     extutil.Ptr(true),
			Options: extutil.Ptr([]action_kit_api.ParameterOption{
				action_kit_api.ExplicitParameterOption{Label: "NXDOMAIN", Value: "NXDOMAIN"},
				action_kit_api.ExplicitParameterOption{Label: "SERVFAIL", Value: "SERVFAIL"},
				action_kit_api.ExplicitParameterOption{Label: "TIMEOUT", Value: "TIMEOUT"},
			}),
			Order: extutil.Ptr(1),
		},
		{
			Name:         "port",
			Label:        "DNS Port",
			Description:  extutil.Ptr("DNS port or port range to intercept (e.g. 53 or 1-65535)."),
			Type:         action_kit_api.ActionParameterTypeString,
			DefaultValue: extutil.Ptr("53"),
			Required:     extutil.Ptr(false),
			Order:        extutil.Ptr(2),
		},
		{
			Name:        "cidr",
			Label:       "Target CIDRs",
			Description: extutil.Ptr("IP CIDRs to match. If empty, all DNS traffic is affected."),
			Type:        action_kit_api.ActionParameterTypeStringArray,
			Required:    extutil.Ptr(false),
			Order:       extutil.Ptr(3),
		},
		{
			Name:         "failOnHostNetwork",
			Label:        "Fail on Host Network",
			Description:  extutil.Ptr("Should the action fail if the container is using host network?"),
			Type:         action_kit_api.ActionParameterTypeBoolean,
			DefaultValue: extutil.Ptr("true"),
			Required:     extutil.Ptr(true),
			Order:        extutil.Ptr(100),
		},
	}
}

func parseDNSInjectOpts(config map[string]interface{}) (dnsinject.Opts, error) {
	errorTypeStrings := extutil.ToStringArray(config["dnsErrorType"])
	if len(errorTypeStrings) == 0 {
		return dnsinject.Opts{}, fmt.Errorf("at least one DNS error type must be selected")
	}

	var errorTypes []dnsinject.ErrorType
	for _, s := range errorTypeStrings {
		switch s {
		case "NXDOMAIN":
			errorTypes = append(errorTypes, dnsinject.ErrorTypeNXDOMAIN)
		case "SERVFAIL":
			errorTypes = append(errorTypes, dnsinject.ErrorTypeSERVFAIL)
		case "TIMEOUT":
			errorTypes = append(errorTypes, dnsinject.ErrorTypeTimeout)
		default:
			return dnsinject.Opts{}, fmt.Errorf("invalid DNS error type: %s", s)
		}
	}

	portStr := extutil.ToString(config["port"])
	if portStr == "" {
		portStr = "53"
	}
	portRange, err := network.ParsePortRange(portStr)
	if err != nil {
		return dnsinject.Opts{}, fmt.Errorf("invalid port: %w", err)
	}

	var cidrs []net.IPNet
	cidrStrings := extutil.ToStringArray(config["cidr"])
	for _, s := range cidrStrings {
		cidr, err := network.ParseCIDR(s)
		if err != nil {
			return dnsinject.Opts{}, fmt.Errorf("invalid CIDR %q: %w", s, err)
		}
		cidrs = append(cidrs, *cidr)
	}

	return dnsinject.Opts{
		ErrorTypes: errorTypes,
		CIDRs:      cidrs,
		PortRange:  portRange,
	}, nil
}

func formatDNSMetricsMessages(metrics *dnsinject.Metrics) []action_kit_api.Message {
	markdown := fmt.Sprintf(`### Packets Processed
- **Total Packets:** %d
- **DNS Requests Matched:** %d

### Injections by Type
- **NXDOMAIN:** %d
- **SERVFAIL:** %d
- **TIMEOUT:** %d
- **Total Injected:** %d`,
		metrics.Seen,
		metrics.DnsMatched,
		metrics.InjectedNxdomain,
		metrics.InjectedServfail,
		metrics.InjectedTimeout,
		metrics.Injected,
	)

	now := time.Now()
	messageType := "dns_stats_markdown"
	return []action_kit_api.Message{
		{
			Message:   markdown,
			Timestamp: &now,
			Type:      &messageType,
		},
	}
}

func getDNSInjectHandle(executionId string) (dnsinject.DNSInject, bool) {
	dnsInjectHandlesLock.Lock()
	defer dnsInjectHandlesLock.Unlock()
	h, ok := dnsInjectHandles[executionId]
	return h, ok
}

func removeDNSInjectHandle(executionId string) {
	dnsInjectHandlesLock.Lock()
	defer dnsInjectHandlesLock.Unlock()
	delete(dnsInjectHandles, executionId)
}
