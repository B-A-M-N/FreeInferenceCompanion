package cli

import (
	"github.com/b-a-m-n/freeinference-companion/internal/runtime"
	"github.com/b-a-m-n/freeinference-companion/internal/tracing"
	"github.com/b-a-m-n/freeinference-companion/pkg/schema"
)

func environmentTraceInfo(client string, activation runtime.Activation) *schema.TraceInfo {
	envTrace, ok := tracing.EnvironmentTrace()
	if !ok || !activation.Active {
		return nil
	}
	if envTrace.Client != "" {
		if activation.Client != "" && string(activation.Client) != envTrace.Client {
			return nil
		}
		client = envTrace.Client
	}
	return &schema.TraceInfo{
		Enabled:        true,
		Verified:       false,
		SessionID:      envTrace.SessionID,
		Source:         string(envTrace.Source),
		Provider:       schema.ProviderFreeInference,
		Client:         client,
		Header:         tracing.SessionHeader,
		EndpointOrigin: activation.Origin,
	}
}

func currentTraceInfo(snap *schema.Snapshot, client string, activation runtime.Activation) *schema.TraceInfo {
	if !activation.Active {
		return nil
	}
	if snap != nil && snap.Trace != nil {
		if snap.ActivationID == "" {
			return nil
		}
		identity, err := activation.Identity(runtime.DefaultSaltLoader())
		if err != nil || identity.DirName() != snap.ActivationID {
			return nil
		}
	}
	if snap != nil && snap.Trace != nil && snap.Trace.Enabled && snap.Provider.Confirmed && snap.Provider.Name == schema.ProviderFreeInference &&
		snap.Trace.Verified && snap.Trace.Provider == schema.ProviderFreeInference && (snap.Trace.Client == "" || snap.Trace.Client == client) &&
		snap.Trace.Header == tracing.SessionHeader && snap.Trace.Source != schema.TraceSourceNone && tracing.ValidateTraceID(snap.Trace.SessionID) {
		copy := *snap.Trace
		return &copy
	}
	return environmentTraceInfo(client, activation)
}
