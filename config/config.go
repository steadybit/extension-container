// Copyright 2025 steadybit GmbH. All rights reserved.

package config

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gobwas/glob"
	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog/log"
)

type Specification struct {
	ContainerSocket             string           `json:"containerSocket" split_words:"true" required:"false"`
	ContainerRuntime            string           `json:"containerRuntime" split_words:"true" required:"false"`
	ContainerdNamespace         string           `json:"containerdNamespace" split_words:"true" required:"true" default:"k8s.io"`
	DisableDiscoveryExcludes    bool             `required:"false" split_words:"true" default:"false"`
	DiscoveryCallInterval       string           `json:"discoveryCallInterval" split_words:"true" required:"false" default:"15s"`
	DiscoveryAttributesExcludes []string         `json:"discoveryAttributesExcludes" split_words:"true" required:"false" default:"container.label.io.buildpacks.lifecycle.metadata,container.label.io.buildpacks.build.metadata"`
	Port                        uint16           `json:"port" split_words:"true" required:"false" default:"8086"`
	HealthPort                  uint16           `json:"healthPort" split_words:"true" required:"false" default:"8082"`
	LivenessCheckInterval       string           `json:"livenessProbeInterval" split_words:"true" required:"false" default:"30s"` // 0 or empty string disables liveness check
	Hostname                    string           `json:"hostname" split_words:"true" required:"false"`
	DisallowHostNetwork         bool             `json:"disallowHostNetwork" split_words:"true" required:"false" default:"false"`
	DisallowK8sNamespaces       []DisallowedName `json:"disallowK8sNamespaces" split_words:"true" required:"false"`
	// NetworkStrictRootQdisc controls how network attacks behave on
	// interfaces whose root qdisc isn't `noqueue` (e.g. the kernel default
	// `mq` on managed-cloud nodes):
	//   - true (default): refuse the attack in the prepare step.
	//   - false: install the attack, but snapshot the root qdisc tree
	//     beforehand and replay it on revert so the cloud-tuned state
	//     (e.g. GKE's `mq + fq` with `buckets=32768 horizon=2s`) is
	//     preserved instead of being reset to kernel defaults.
	// STEADYBIT_EXTENSION_NETWORK_STRICT_ROOT_QDISC
	NetworkStrictRootQdisc bool `json:"networkStrictRootQdisc" split_words:"true" required:"false" default:"true"`
	// TLSInterceptCaCert / TLSInterceptCaKey point at a PEM certificate authority
	// used by 'Intercept Outgoing HTTP Request' to mint per-hostname certificates, which is
	// what lets it return a synthesized response for an HTTPS dependency instead
	// of cleartext HTTP only. Unset (the default) leaves HTTPS untouched.
	//
	// The CA is the customer's: they generate it, choose its validity, and install
	// it in the truststores of the workloads they want to fault. Because it can
	// impersonate any HTTPS endpoint to anything trusting it, mount it from a
	// Secret and keep this to test environments.
	// STEADYBIT_EXTENSION_TLS_INTERCEPT_CA_CERT / _KEY
	TLSInterceptCaCert string `json:"tlsInterceptCaCert" split_words:"true" required:"false"`
	TLSInterceptCaKey  string `json:"tlsInterceptCaKey" split_words:"true" required:"false"`
	// TLSInterceptLeafValidity is how long the per-hostname certificates the
	// proxy mints stay valid. Shorter narrows the window in which a leaf that
	// escaped the proxy could be used; it is always clamped to the CA's own
	// expiry. Empty keeps the proxy's default (24h).
	// STEADYBIT_EXTENSION_TLS_INTERCEPT_LEAF_VALIDITY
	TLSInterceptLeafValidity time.Duration `json:"tlsInterceptLeafValidity" split_words:"true" required:"false"`
}

// TLSInterceptEnabled reports whether HTTPS response injection is configured.
func (s Specification) TLSInterceptEnabled() bool {
	return s.TLSInterceptCaCert != "" && s.TLSInterceptCaKey != ""
}

var (
	Config Specification
)

func ParseConfiguration() {
	if err := envconfig.Process("steadybit_extension", &Config); err != nil {
		log.Fatal().Err(err).Msgf("Failed to parse configuration from environment.")
	}

	if err := parseArgs(&Config); err != nil {
		log.Fatal().Err(err).Msgf("Failed to parse command line arguments.")
	}
}

func parseArgs(cfg *Specification) error {
	f := flag.NewFlagSet("config", flag.ContinueOnError)
	var disallowHostNetwork = f.Bool("disallowHostNetwork", false, "Disallow network attacks on host network containers")
	var disallowK8sNamespaces = f.String("disallowK8sNamespaces", "", "Disallow attacks on these k8s namespaces")

	if err := f.Parse(os.Args[1:]); err != nil {
		return err
	}

	cfg.DisallowHostNetwork = cfg.DisallowHostNetwork || *disallowHostNetwork

	for s := range strings.SplitSeq(strings.TrimSpace(*disallowK8sNamespaces), ",") {
		if s == "" {
			continue
		}
		var d DisallowedName
		if err := d.Decode(s); err != nil {
			return err
		}
		cfg.DisallowK8sNamespaces = append(cfg.DisallowK8sNamespaces, d)
	}

	return nil
}

func ValidateConfiguration() {
	// Half a CA is never usable, and the resulting failure (every interception
	// handshake failing) is far harder to diagnose than refusing to start.
	if (Config.TLSInterceptCaCert == "") != (Config.TLSInterceptCaKey == "") {
		log.Fatal().Msg("STEADYBIT_EXTENSION_TLS_INTERCEPT_CA_CERT and STEADYBIT_EXTENSION_TLS_INTERCEPT_CA_KEY must be set together")
	}
	if err := Config.validateInterceptCA(); err != nil {
		log.Fatal().Msg(err.Error())
	}
	if Config.DisableDiscoveryExcludes {
		log.Info().Msg("Discovery excludes are disabled. Will also discover containers labeled with steadybit.com/discovery-disabled.")
	}
}

type DisallowedName struct {
	p string
	g glob.Glob
}

func (d DisallowedName) String() string {
	return d.p
}

func (d DisallowedName) Match(value string) bool {
	return d.g.Match(value)
}

func (d *DisallowedName) Decode(value string) error {
	g, err := glob.Compile(value)
	if err != nil {
		return err
	}
	d.g = g
	d.p = value
	return nil
}

// validateInterceptCA checks the configured CA is actually loadable. Without
// this, a path that does not exist or a Secret key holding something other than
// a keypair passes startup and only surfaces per-attack — or, worse, as HTTPS
// quietly flowing through untouched while the UI says interception is on.
func (s Specification) validateInterceptCA() error {
	if !s.TLSInterceptEnabled() {
		return nil
	}
	certPEM, err := os.ReadFile(s.TLSInterceptCaCert)
	if err != nil {
		return fmt.Errorf("cannot read STEADYBIT_EXTENSION_TLS_INTERCEPT_CA_CERT: %w", err)
	}
	keyPEM, err := os.ReadFile(s.TLSInterceptCaKey)
	if err != nil {
		return fmt.Errorf("cannot read STEADYBIT_EXTENSION_TLS_INTERCEPT_CA_KEY: %w", err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return fmt.Errorf("the configured TLS interception CA is not a usable certificate/key pair: %w", err)
	}
	return nil
}
