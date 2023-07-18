package promtailconvert

import (
	"bytes"
	"flag"
	"fmt"

	"github.com/alecthomas/units"
	"github.com/grafana/agent/component/common/loki"
	fnet "github.com/grafana/agent/component/common/net"
	"github.com/grafana/agent/component/loki/source/api"
	lokiwrite "github.com/grafana/agent/component/loki/write"
	"github.com/grafana/agent/converter/diag"
	"github.com/grafana/agent/converter/internal/common"
	"github.com/grafana/agent/converter/internal/prometheusconvert"
	"github.com/grafana/agent/converter/internal/promtailconvert/internal/build"
	"github.com/grafana/agent/pkg/river/token/builder"
	"github.com/grafana/dskit/flagext"
	"github.com/grafana/loki/clients/pkg/promtail/client"
	promtailcfg "github.com/grafana/loki/clients/pkg/promtail/config"
	"github.com/grafana/loki/clients/pkg/promtail/limit"
	"github.com/grafana/loki/clients/pkg/promtail/positions"
	"github.com/grafana/loki/clients/pkg/promtail/scrapeconfig"
	"github.com/grafana/loki/clients/pkg/promtail/server"
	lokicfgutil "github.com/grafana/loki/pkg/util/cfg"
	lokiflag "github.com/grafana/loki/pkg/util/flagext"
	"gopkg.in/yaml.v2"
)

type Config struct {
	promtailcfg.Config `yaml:",inline"`
}

// Clone takes advantage of pass-by-value semantics to return a distinct *Config.
// This is primarily used to parse a different flag set without mutating the original *Config.
func (c *Config) Clone() flagext.Registerer {
	return func(c Config) *Config {
		return &c
	}(*c)
}

// Convert implements a Promtail config converter.
func Convert(in []byte) ([]byte, diag.Diagnostics) {
	var (
		diags diag.Diagnostics
		cfg   Config
	)

	// Set default values first.
	flagSet := flag.NewFlagSet("", flag.PanicOnError)
	err := lokicfgutil.Unmarshal(&cfg,
		lokicfgutil.Defaults(flagSet),
	)
	if err != nil {
		diags.Add(diag.SeverityLevelCritical, fmt.Sprintf("failed to set default Promtail config values: %s", err))
		return nil, diags
	}

	// Unmarshall explicitly specified values
	if err := yaml.UnmarshalStrict(in, &cfg); err != nil {
		diags.Add(diag.SeverityLevelCritical, fmt.Sprintf("failed to parse Promtail config: %s", err))
		return nil, diags
	}

	// Replicate promtails' handling of this deprecated field.
	if cfg.ClientConfig.URL.URL != nil {
		// if a single client config is used we add it to the multiple client config for backward compatibility
		cfg.ClientConfigs = append(cfg.ClientConfigs, cfg.ClientConfig)
	}

	f := builder.NewFile()
	diags = AppendAll(f, &cfg.Config, diags)

	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		diags.Add(diag.SeverityLevelCritical, fmt.Sprintf("failed to render Flow config: %s", err.Error()))
		return nil, diags
	}

	if len(buf.Bytes()) == 0 {
		return nil, diags
	}

	prettyByte, newDiags := common.PrettyPrint(buf.Bytes())
	diags = append(diags, newDiags...)
	return prettyByte, diags
}

// AppendAll analyzes the entire promtail config in memory and transforms it
// into Flow components. It then appends each argument to the file builder.
func AppendAll(f *builder.File, cfg *promtailcfg.Config, diags diag.Diagnostics) diag.Diagnostics {
	validateTopLevelConfig(cfg, &diags)

	var writeReceivers = make([]loki.LogsReceiver, len(cfg.ClientConfigs))
	var writeBlocks = make([]*builder.Block, len(cfg.ClientConfigs))
	// Each client config needs to be a separate remote_write,
	// because they may have different ExternalLabels fields.
	for i, cc := range cfg.ClientConfigs {
		writeBlocks[i], writeReceivers[i] = newLokiWrite(&cc, &diags, i)
	}

	gc := &build.GlobalContext{
		WriteReceivers:   writeReceivers,
		TargetSyncPeriod: cfg.TargetConfig.SyncPeriod,
	}

	for _, sc := range cfg.ScrapeConfig {
		appendScrapeConfig(f, &sc, &diags, gc)
	}

	for _, write := range writeBlocks {
		f.Body().AppendBlock(write)
	}

	return diags
}

func defaultPositionsConfig() positions.Config {
	// We obtain the default by registering the flags
	cfg := positions.Config{}
	cfg.RegisterFlags(flag.NewFlagSet("", flag.PanicOnError))
	return cfg
}

func defaultLimitsConfig() limit.Config {
	cfg := limit.Config{}
	cfg.RegisterFlagsWithPrefix("", flag.NewFlagSet("", flag.PanicOnError))
	return cfg
}

func appendScrapeConfig(
	f *builder.File,
	cfg *scrapeconfig.Config,
	diags *diag.Diagnostics,
	gctx *build.GlobalContext,
) {
	//TODO(thampiotr): need to support/warn about the following fields:
	//JobName              string                      `mapstructure:"job_name,omitempty" yaml:"job_name,omitempty"`
	//Encoding               string                 `mapstructure:"encoding,omitempty" yaml:"encoding,omitempty"`
	//DecompressionCfg       *DecompressionConfig   `yaml:"decompression,omitempty"`

	//TODO(thampiotr): support/warn about the following log producing promtail configs:
	//SyslogConfig         *SyslogTargetConfig         `mapstructure:"syslog,omitempty" yaml:"syslog,omitempty"`
	//GcplogConfig         *GcplogTargetConfig         `mapstructure:"gcplog,omitempty" yaml:"gcplog,omitempty"`
	//PushConfig           *PushTargetConfig           `mapstructure:"loki_push_api,omitempty" yaml:"loki_push_api,omitempty"`
	//WindowsConfig        *WindowsEventsTargetConfig  `mapstructure:"windows_events,omitempty" yaml:"windows_events,omitempty"`
	//KafkaConfig          *KafkaTargetConfig          `mapstructure:"kafka,omitempty" yaml:"kafka,omitempty"`
	//AzureEventHubsConfig *AzureEventHubsTargetConfig `mapstructure:"azure_event_hubs,omitempty" yaml:"azure_event_hubs,omitempty"`
	//GelfConfig           *GelfTargetConfig           `mapstructure:"gelf,omitempty" yaml:"gelf,omitempty"`
	//HerokuDrainConfig    *HerokuDrainTargetConfig    `mapstructure:"heroku_drain,omitempty" yaml:"heroku_drain,omitempty"`

	//TODO(thampiotr): support/warn about the following SD configs:
	//// List of labeled target groups for this job.
	//StaticConfigs discovery.StaticConfig `mapstructure:"static_configs" yaml:"static_configs"`
	//// List of file service discovery configurations.
	//FileSDConfigs []*file.SDConfig `mapstructure:"file_sd_configs,omitempty" yaml:"file_sd_configs,omitempty"`
	//// List of Consul service discovery configurations.
	//ConsulSDConfigs []*consul.SDConfig `mapstructure:"consul_sd_configs,omitempty" yaml:"consul_sd_configs,omitempty"`
	//// List of Consul agent service discovery configurations.
	//ConsulAgentSDConfigs []*consulagent.SDConfig `mapstructure:"consulagent_sd_configs,omitempty" yaml:"consulagent_sd_configs,omitempty"`
	//// List of Kubernetes service discovery configurations.
	//KubernetesSDConfigs []*kubernetes.SDConfig `mapstructure:"kubernetes_sd_configs,omitempty" yaml:"kubernetes_sd_configs,omitempty"`
	// TODO: ==== undocumented SDs - if they exist in Flow, we support, if they don't we log warning ====
	//// List of DigitalOcean service discovery configurations.
	//DigitalOceanSDConfigs []*digitalocean.SDConfig `mapstructure:"digitalocean_sd_configs,omitempty" yaml:"digitalocean_sd_configs,omitempty"`
	//// List of Docker Swarm service discovery configurations.
	//DockerSwarmSDConfigs []*moby.DockerSwarmSDConfig `mapstructure:"dockerswarm_sd_configs,omitempty" yaml:"dockerswarm_sd_configs,omitempty"`
	//// List of Serverset service discovery configurations.
	//ServersetSDConfigs []*zookeeper.ServersetSDConfig `mapstructure:"serverset_sd_configs,omitempty" yaml:"serverset_sd_configs,omitempty"`
	//// NerveSDConfigs is a list of Nerve service discovery configurations.
	//NerveSDConfigs []*zookeeper.NerveSDConfig `mapstructure:"nerve_sd_configs,omitempty" yaml:"nerve_sd_configs,omitempty"`
	//// MarathonSDConfigs is a list of Marathon service discovery configurations.
	//MarathonSDConfigs []*marathon.SDConfig `mapstructure:"marathon_sd_configs,omitempty" yaml:"marathon_sd_configs,omitempty"`
	//// List of GCE service discovery configurations.
	//GCESDConfigs []*gce.SDConfig `mapstructure:"gce_sd_configs,omitempty" yaml:"gce_sd_configs,omitempty"`
	//// List of EC2 service discovery configurations.
	//EC2SDConfigs []*aws.EC2SDConfig `mapstructure:"ec2_sd_configs,omitempty" yaml:"ec2_sd_configs,omitempty"`
	//// List of OpenStack service discovery configurations.
	//OpenstackSDConfigs []*openstack.SDConfig `mapstructure:"openstack_sd_configs,omitempty" yaml:"openstack_sd_configs,omitempty"`
	//// List of Azure service discovery configurations.
	//AzureSDConfigs []*azure.SDConfig `mapstructure:"azure_sd_configs,omitempty" yaml:"azure_sd_configs,omitempty"`
	//// List of Triton service discovery configurations.
	//TritonSDConfigs []*triton.SDConfig `mapstructure:"triton_sd_configs,omitempty" yaml:"triton_sd_configs,omitempty"`

	b := build.NewScrapeConfigBuilder(f, diags, cfg, gctx)

	// Append all the SD components
	b.AppendKubernetesSDs()
	b.AppendDockerSDs()
	b.AppendStaticSDs()

	// Append loki.source.file to process all SD components' targets.
	// If any relabelling is required, it will be done via a discovery.relabel component.
	// The files will be watched and the globs in file paths will be expanded using discovery.file component.
	// The log entries are sent to loki.process if processing is needed, or directly to loki.write components.
	b.AppendLokiSourceFile()

	// Append all the components that produce logs directly.
	// If any relabelling is required, it will be done via a loki.relabel component.
	// The logs are sent to loki.process if processing is needed, or directly to loki.write components.
	//TODO(thampiotr): add support for other integrations
	b.AppendCloudFlareConfig()
	b.AppendJournalConfig()
}

func newLokiWrite(client *client.Config, diags *diag.Diagnostics, index int) (*builder.Block, loki.LogsReceiver) {
	label := fmt.Sprintf("default_%d", index)
	lokiWriteArgs := toLokiWriteArguments(client, diags)
	block := common.NewBlockWithOverride([]string{"loki", "write"}, label, lokiWriteArgs)
	return block, common.ConvertLogsReceiver{
		Expr: fmt.Sprintf("loki.write.%s.receiver", label),
	}
}

func appendServerConfig(f *builder.File, config server.Config, receivers []loki.LogsReceiver, diags *diag.Diagnostics) {
	lokiHttpArgs := toLokiApiArguments(config, receivers, diags)
	//TODO(thampiotr): this will be used for scrape_configs.loki_push_api.server once we add support
	_ = lokiHttpArgs
}

func toLokiApiArguments(config server.Config, receivers []loki.LogsReceiver, diags *diag.Diagnostics) api.Arguments {
	if config.ProfilingEnabled {
		diags.Add(diag.SeverityLevelWarn, "server.profiling_enabled is not supported - use Agent's "+
			"main HTTP server's profiling endpoints instead.")
	}

	if config.RegisterInstrumentation {
		diags.Add(diag.SeverityLevelWarn, "server.register_instrumentation is not supported - Flow mode "+
			"components expose their metrics automatically in their own metrics namespace")
	}

	if config.LogLevel.String() != "info" {
		diags.Add(diag.SeverityLevelWarn, "server.log_level is not supported - Flow mode "+
			"components may produce different logs")
	}

	if config.PathPrefix != "" {
		diags.Add(diag.SeverityLevelWarn, "server.http_path_prefix is not supported - Flow mode's "+
			"loki.source.api is available at /api/v1/push - see documentation for more details. If you are sending "+
			"logs to this endpoint, the clients configuration may need to be updated.")
	}

	if config.HealthCheckTarget != nil && !*config.HealthCheckTarget {
		diags.Add(diag.SeverityLevelWarn, "server.health_check_target disabling is not supported in Flow mode")
	}

	forwardTo := receivers
	return api.Arguments{
		Server: &fnet.ServerConfig{
			HTTP: &fnet.HTTPConfig{
				ListenAddress:      config.HTTPListenAddress,
				ListenPort:         config.HTTPListenPort,
				ConnLimit:          config.HTTPConnLimit,
				ServerReadTimeout:  config.HTTPServerReadTimeout,
				ServerWriteTimeout: config.HTTPServerWriteTimeout,
				ServerIdleTimeout:  config.HTTPServerIdleTimeout,
			},
			GRPC: &fnet.GRPCConfig{
				ListenAddress:              config.GRPCListenAddress,
				ListenPort:                 config.GRPCListenPort,
				ConnLimit:                  config.GRPCConnLimit,
				MaxConnectionAge:           config.GRPCServerMaxConnectionAge,
				MaxConnectionAgeGrace:      config.GRPCServerMaxConnectionAgeGrace,
				MaxConnectionIdle:          config.GRPCServerMaxConnectionIdle,
				ServerMaxRecvMsg:           config.GPRCServerMaxRecvMsgSize,
				ServerMaxSendMsg:           config.GRPCServerMaxSendMsgSize,
				ServerMaxConcurrentStreams: config.GPRCServerMaxConcurrentStreams,
			},
			GracefulShutdownTimeout: config.ServerGracefulShutdownTimeout,
		},
		ForwardTo: forwardTo,
	}
}

func toLokiWriteArguments(config *client.Config, diags *diag.Diagnostics) *lokiwrite.Arguments {
	batchSize, err := units.ParseBase2Bytes(fmt.Sprintf("%dB", config.BatchSize))
	if err != nil {
		diags.Add(
			diag.SeverityLevelError,
			fmt.Sprintf("failed to parse BatchSize for client config %s: %s", config.Name, err.Error()),
		)
	}

	// This is not supported yet - see https://github.com/grafana/agent/issues/4335.
	if config.DropRateLimitedBatches {
		diags.Add(
			diag.SeverityLevelError,
			"DropRateLimitedBatches is currently not supported in Grafana Agent Flow.",
		)
	}

	// Also deprecated in promtail.
	if len(config.StreamLagLabels) != 0 {
		diags.Add(
			diag.SeverityLevelWarn,
			"stream_lag_labels is deprecated and the associated metric has been removed",
		)
	}

	return &lokiwrite.Arguments{
		Endpoints: []lokiwrite.EndpointOptions{
			{
				Name:              config.Name,
				URL:               config.URL.String(),
				BatchWait:         config.BatchWait,
				BatchSize:         batchSize,
				HTTPClientConfig:  prometheusconvert.ToHttpClientConfig(&config.Client),
				Headers:           config.Headers,
				MinBackoff:        config.BackoffConfig.MinBackoff,
				MaxBackoff:        config.BackoffConfig.MaxBackoff,
				MaxBackoffRetries: config.BackoffConfig.MaxRetries,
				RemoteTimeout:     config.Timeout,
				TenantID:          config.TenantID,
			},
		},
		ExternalLabels: convertFlagLabels(config.ExternalLabels),
	}
}

func convertFlagLabels(labels lokiflag.LabelSet) map[string]string {
	result := map[string]string{}
	for k, v := range labels.LabelSet {
		result[string(k)] = string(v)
	}
	return result
}
