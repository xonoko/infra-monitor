// Package config loads infra-monitor configuration from a YAML file and
// INFRA_MONITOR_* environment variables. Secret values can be read from files
// through the matching *_file keys.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// EnvPrefix is the prefix of every environment override.
const EnvPrefix = "INFRA_MONITOR"

type Config struct {
	Env       string `mapstructure:"env"` // production | development
	LogLevel  string `mapstructure:"log_level"`
	LogFormat string `mapstructure:"log_format"` // json | text
	// CAFile is an extra PEM CA bundle trusted by every outbound HTTPS client (Holmes, Alertmanager, OIDC, pings...).
	CAFile      string            `mapstructure:"ca_file"`
	Server      ServerConfig      `mapstructure:"server"`
	Database    DatabaseConfig    `mapstructure:"database"`
	Telemetry   TelemetryConfig   `mapstructure:"telemetry"`
	Auth        AuthConfig        `mapstructure:"auth"`
	Sources     []SourceConfig    `mapstructure:"sources"`
	Heartbeat   HeartbeatConfig   `mapstructure:"heartbeat"`
	Correlation CorrelationConfig `mapstructure:"correlation"`
	Agent       AgentConfig       `mapstructure:"agent"`
	Actions     ActionsConfig     `mapstructure:"actions"`
	Channels    []ChannelConfig   `mapstructure:"channels"`
	Routing     []RouteConfig     `mapstructure:"routing"`
	Retention   RetentionConfig   `mapstructure:"retention"`
	Jobs        JobsConfig        `mapstructure:"jobs"`
}

type ServerConfig struct {
	Listen          string        `mapstructure:"listen"`
	PublicURL       string        `mapstructure:"public_url"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	// TrustedProxies enables X-Forwarded-* handling (set when running behind an ingress).
	TrustProxy bool `mapstructure:"trust_proxy"`
	// TLS serves HTTPS when cert_file and key_file are set (reloaded automatically when the files change).
	TLS ServerTLSConfig `mapstructure:"tls"`
}

type ServerTLSConfig struct {
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

type DatabaseConfig struct {
	DSN          string `mapstructure:"dsn"`
	DSNFile      string `mapstructure:"dsn_file"`
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	Name         string `mapstructure:"name"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	PasswordFile string `mapstructure:"password_file"`
	SSLMode      string `mapstructure:"sslmode"`
	// SSLRootCert is the CA bundle used with sslmode verify-ca / verify-full.
	SSLRootCert string `mapstructure:"sslrootcert"`
	MaxConns    int32  `mapstructure:"max_conns"`
	// MigrateOnStart applies pending migrations when `serve` starts (guarded by an advisory lock).
	MigrateOnStart bool `mapstructure:"migrate_on_start"`
}

type TelemetryConfig struct {
	ServiceName string        `mapstructure:"service_name"`
	Tracing     TracingConfig `mapstructure:"tracing"`
	Metrics     MetricsConfig `mapstructure:"metrics"`
}

type TracingConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Exporter: otlp-grpc | otlp-http | stdout. Endpoint/headers fall back to OTEL_EXPORTER_OTLP_* env vars.
	Exporter    string            `mapstructure:"exporter"`
	Endpoint    string            `mapstructure:"endpoint"`
	Insecure    bool              `mapstructure:"insecure"`
	Headers     map[string]string `mapstructure:"headers"`
	SampleRatio float64           `mapstructure:"sample_ratio"`
	// UIURLTemplate builds "view trace" links in the UI, e.g. https://hyperdx.example.com/trace/{trace_id}
	UIURLTemplate string `mapstructure:"ui_url_template"`
}

type MetricsConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// OTLP push in addition to the Prometheus /metrics endpoint.
	OTLPEnabled  bool          `mapstructure:"otlp_enabled"`
	OTLPEndpoint string        `mapstructure:"otlp_endpoint"`
	OTLPInterval time.Duration `mapstructure:"otlp_interval"`
}

type AuthConfig struct {
	Local      LocalAuthConfig `mapstructure:"local"`
	OIDC       OIDCConfig      `mapstructure:"oidc"`
	SessionTTL time.Duration   `mapstructure:"session_ttl"`
	// CookieSecure forces the Secure flag; defaults to true when public_url is https.
	CookieSecure *bool `mapstructure:"cookie_secure"`
}

type LocalAuthConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

type OIDCConfig struct {
	Enabled          bool     `mapstructure:"enabled"`
	Name             string   `mapstructure:"name"` // label on the login button
	Issuer           string   `mapstructure:"issuer"`
	ClientID         string   `mapstructure:"client_id"`
	ClientSecret     string   `mapstructure:"client_secret"`
	ClientSecretFile string   `mapstructure:"client_secret_file"`
	RedirectURL      string   `mapstructure:"redirect_url"`
	Scopes           []string `mapstructure:"scopes"`
	GroupsClaim      string   `mapstructure:"groups_claim"`
	AdminGroups      []string `mapstructure:"admin_groups"`
	OperatorGroups   []string `mapstructure:"operator_groups"`
	// DefaultRole for users matching no group: viewer | none (none = login refused).
	DefaultRole string `mapstructure:"default_role"`
}

type SourceAuthConfig struct {
	BearerToken       string `mapstructure:"bearer_token"`
	BearerTokenFile   string `mapstructure:"bearer_token_file"`
	BasicUsername     string `mapstructure:"basic_username"`
	BasicPassword     string `mapstructure:"basic_password"`
	BasicPasswordFile string `mapstructure:"basic_password_file"`
}

type SourceConfig struct {
	Name string           `mapstructure:"name"`
	Type string           `mapstructure:"type"` // alertmanager
	Auth SourceAuthConfig `mapstructure:"auth"`
	// AlertmanagerURL is used for reconcile (GET /api/v2/alerts) and silences.
	AlertmanagerURL   string        `mapstructure:"alertmanager_url"`
	ReconcileInterval time.Duration `mapstructure:"reconcile_interval"`
	ClusterLabel      string        `mapstructure:"cluster_label"`
	NamespaceLabel    string        `mapstructure:"namespace_label"`
	// DefaultCluster is used when signals have no cluster label.
	DefaultCluster string `mapstructure:"default_cluster"`
}

type HeartbeatConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Matchers select heartbeat signals, e.g. ["alertname=Watchdog"].
	Matchers         []string      `mapstructure:"matchers"`
	ExpectedInterval time.Duration `mapstructure:"expected_interval"`
	SilentAfter      time.Duration `mapstructure:"silent_after"`
	PingURL          string        `mapstructure:"ping_url"`
	PingURLFile      string        `mapstructure:"ping_url_file"`
	PingMethod       string        `mapstructure:"ping_method"`
	PingInterval     time.Duration `mapstructure:"ping_interval"`
}

type CorrelationConfig struct {
	GroupBy      []string      `mapstructure:"group_by"`
	ReopenWindow time.Duration `mapstructure:"reopen_window"`
	Debounce     time.Duration `mapstructure:"debounce"`
	ResolveGrace time.Duration `mapstructure:"resolve_grace"`
}

type AgentConfig struct {
	Enabled    bool          `mapstructure:"enabled"`
	Type       string        `mapstructure:"type"` // holmes
	URL        string        `mapstructure:"url"`
	APIKey     string        `mapstructure:"api_key"`
	APIKeyFile string        `mapstructure:"api_key_file"`
	Model      string        `mapstructure:"model"`
	Timeout    time.Duration `mapstructure:"timeout"`
	// NotifyTimeout: notify without AI after this delay (fail open).
	NotifyTimeout time.Duration `mapstructure:"notify_timeout"`
	MaxConcurrent int           `mapstructure:"max_concurrent"`
	MaxPerHour    int           `mapstructure:"max_per_hour"`
	Cooldown      time.Duration `mapstructure:"cooldown"`
	// DeepModel is used only when a user explicitly asks for a deeper investigation.
	DeepModel string `mapstructure:"deep_model"`
	// ChatModel answers chat follow-ups (default: deep_model).
	ChatModel string `mapstructure:"chat_model"`
	MaxPerDay int    `mapstructure:"max_per_day"`
	// InvestigateSeverities gates automatic investigations (manual ones are always allowed).
	InvestigateSeverities []string    `mapstructure:"investigate_severities"`
	Storm                 StormConfig `mapstructure:"storm"`
	// BudgetPause stops all agent calls after a budget/rate-limit error.
	BudgetPause time.Duration `mapstructure:"budget_pause"`
	// Prices (USD per million tokens) for the cost estimate metric.
	PriceInputPerMTok  float64 `mapstructure:"price_input_per_mtok"`
	PriceOutputPerMTok float64 `mapstructure:"price_output_per_mtok"`
	// ModelPrices overrides the prices per model name (e.g. the deep model).
	ModelPrices      map[string]ModelPrice `mapstructure:"model_prices"`
	ExtraPrompt      string                `mapstructure:"extra_prompt"`
	StructuredOutput bool                  `mapstructure:"structured_output"`
}

// ModelPrice is USD per million tokens.
type ModelPrice struct {
	Input  float64 `mapstructure:"input"`
	Output float64 `mapstructure:"output"`
}

// GenericActionsConfig enables arbitrary k8s changes with precise human approval.
type GenericActionsConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// ApprovedServiceAccount is impersonated for approved generic actions, e.g.
	// "system:serviceaccount:infra-monitor:infra-monitor-approved". Empty = use the pod's own account.
	ApprovedServiceAccount string `mapstructure:"approved_service_account"`
	ExecEnabled            bool   `mapstructure:"exec_enabled"`
	// ExecTimeout bounds kubectl.exec commands.
	ExecTimeout time.Duration `mapstructure:"exec_timeout"`
}

// GitOpsConfig enables gitops.change proposals: commits to a GitHub repository.
type GitOpsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Repo    string `mapstructure:"repo"`   // owner/name
	Branch  string `mapstructure:"branch"` // default main
	// PathPrefixes limits the files that can be changed (empty = whole repo).
	PathPrefixes []string `mapstructure:"path_prefixes"`
	Token        string   `mapstructure:"token"`
	TokenFile    string   `mapstructure:"token_file"`
	APIURL       string   `mapstructure:"api_url"` // default https://api.github.com
	AuthorName   string   `mapstructure:"author_name"`
	AuthorEmail  string   `mapstructure:"author_email"`
}

// StormConfig: when more than Threshold incidents open within Window, they are grouped into one
// "alert storm" incident investigated once, instead of one investigation each.
type StormConfig struct {
	Enabled   bool          `mapstructure:"enabled"`
	Threshold int           `mapstructure:"threshold"`
	Window    time.Duration `mapstructure:"window"`
}

type ActionsConfig struct {
	Enabled        []string          `mapstructure:"enabled"`
	AutoApprove    []string          `mapstructure:"auto_approve"`
	NamespaceDeny  []string          `mapstructure:"namespace_deny"`
	NamespaceAllow []string          `mapstructure:"namespace_allow"`
	Kubeconfigs    map[string]string `mapstructure:"kubeconfigs"`
	// InClusterName is the cluster label value served by the in-cluster ServiceAccount ("" = any cluster without a kubeconfig).
	InClusterName string        `mapstructure:"in_cluster_name"`
	InCluster     bool          `mapstructure:"in_cluster"`
	ProposalTTL   time.Duration `mapstructure:"proposal_ttl"`
	MaxReplicas   int           `mapstructure:"max_replicas"`
	MaxSilence    time.Duration `mapstructure:"max_silence"`
	Timeout       time.Duration `mapstructure:"timeout"`
	// MaxAutoPerHour bounds automatic (auto-approved) executions per incident per hour; then a human decides.
	MaxAutoPerHour int `mapstructure:"max_auto_per_hour"`
	// AllowReinvestigateChain lets an investigation triggered by an action propose another re-investigation.
	AllowReinvestigateChain bool `mapstructure:"allow_reinvestigate_chain"`
	// Generic approved actions (k8s.patch / k8s.apply / k8s.delete / kubectl.exec): never auto-approved, executed
	// by impersonating ApprovedServiceAccount ("system:serviceaccount:<ns>:<name>"), dry-run first.
	Generic GenericActionsConfig `mapstructure:"generic"`
	GitOps  GitOpsConfig         `mapstructure:"gitops"`
	// AnnotationPrefix of the namespace annotations that override the auto-approve policy:
	// <prefix>/auto-approve ("type,type" or "none") and <prefix>/max-auto-per-hour.
	AnnotationPrefix string `mapstructure:"annotation_prefix"`
}

type ChannelConfig struct {
	Name              string `mapstructure:"name"`
	Type              string `mapstructure:"type"` // telegram
	BotToken          string `mapstructure:"bot_token"`
	BotTokenFile      string `mapstructure:"bot_token_file"`
	ChatID            int64  `mapstructure:"chat_id"`
	Mode              string `mapstructure:"mode"` // polling | webhook
	WebhookSecret     string `mapstructure:"webhook_secret"`
	WebhookSecretFile string `mapstructure:"webhook_secret_file"`
	// APIURL overrides the Bot API base URL (tests, local bot API servers).
	APIURL string `mapstructure:"api_url"`
	// MessagesPerMinute caps sends to the chat (Telegram allows ~20/min in groups).
	MessagesPerMinute int `mapstructure:"messages_per_minute"`
}

type RouteConfig struct {
	Name       string   `mapstructure:"name"`
	Match      []string `mapstructure:"match"`      // label matchers, all must match
	Verdicts   []string `mapstructure:"verdicts"`   // empty = any
	Severities []string `mapstructure:"severities"` // empty = any
	Channels   []string `mapstructure:"channels"`
	Continue   bool     `mapstructure:"continue"`
}

type RetentionConfig struct {
	ResolvedIncidents time.Duration `mapstructure:"resolved_incidents"`
	Jobs              time.Duration `mapstructure:"jobs"`
}

type JobsConfig struct {
	Workers      int           `mapstructure:"workers"`
	PollInterval time.Duration `mapstructure:"poll_interval"`
	LockTimeout  time.Duration `mapstructure:"lock_timeout"`
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("env", "production")
	v.SetDefault("log_level", "info")
	v.SetDefault("log_format", "json")

	v.SetDefault("server.listen", ":8080")
	v.SetDefault("server.public_url", "http://localhost:8080")
	v.SetDefault("server.shutdown_timeout", 20*time.Second)
	v.SetDefault("server.trust_proxy", false)

	v.SetDefault("database.dsn", "")
	v.SetDefault("database.dsn_file", "")
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.name", "infra_monitor")
	v.SetDefault("database.user", "infra_monitor")
	v.SetDefault("database.password", "")
	v.SetDefault("database.password_file", "")
	v.SetDefault("database.sslmode", "require")
	v.SetDefault("database.sslrootcert", "")
	v.SetDefault("ca_file", "")
	v.SetDefault("server.tls.cert_file", "")
	v.SetDefault("server.tls.key_file", "")
	v.SetDefault("database.max_conns", 20)
	v.SetDefault("database.migrate_on_start", true)

	v.SetDefault("telemetry.service_name", "infra-monitor")
	v.SetDefault("telemetry.tracing.enabled", false)
	v.SetDefault("telemetry.tracing.exporter", "otlp-grpc")
	v.SetDefault("telemetry.tracing.endpoint", "")
	v.SetDefault("telemetry.tracing.insecure", true)
	v.SetDefault("telemetry.tracing.sample_ratio", 1.0)
	v.SetDefault("telemetry.tracing.ui_url_template", "")
	v.SetDefault("telemetry.metrics.enabled", true)
	v.SetDefault("telemetry.metrics.otlp_enabled", false)
	v.SetDefault("telemetry.metrics.otlp_endpoint", "")
	v.SetDefault("telemetry.metrics.otlp_interval", 30*time.Second)

	v.SetDefault("auth.local.enabled", true)
	v.SetDefault("auth.oidc.enabled", false)
	v.SetDefault("auth.oidc.name", "SSO")
	v.SetDefault("auth.oidc.issuer", "")
	v.SetDefault("auth.oidc.client_id", "")
	v.SetDefault("auth.oidc.client_secret", "")
	v.SetDefault("auth.oidc.client_secret_file", "")
	v.SetDefault("auth.oidc.redirect_url", "")
	v.SetDefault("auth.oidc.scopes", []string{"openid", "profile", "email", "groups"})
	v.SetDefault("auth.oidc.groups_claim", "groups")
	v.SetDefault("auth.oidc.default_role", "viewer")
	v.SetDefault("auth.session_ttl", 7*24*time.Hour)

	v.SetDefault("heartbeat.enabled", true)
	v.SetDefault("heartbeat.matchers", []string{"alertname=Watchdog"})
	v.SetDefault("heartbeat.expected_interval", time.Minute)
	v.SetDefault("heartbeat.silent_after", 10*time.Minute)
	v.SetDefault("heartbeat.ping_url", "")
	v.SetDefault("heartbeat.ping_url_file", "")
	v.SetDefault("heartbeat.ping_method", "GET")
	v.SetDefault("heartbeat.ping_interval", time.Minute)

	v.SetDefault("correlation.group_by", []string{"alertname", "cluster", "namespace"})
	v.SetDefault("correlation.reopen_window", 30*time.Minute)
	v.SetDefault("correlation.debounce", 45*time.Second)
	v.SetDefault("correlation.resolve_grace", 2*time.Minute)

	v.SetDefault("agent.enabled", false)
	v.SetDefault("agent.type", "holmes")
	v.SetDefault("agent.url", "")
	v.SetDefault("agent.api_key", "")
	v.SetDefault("agent.api_key_file", "")
	v.SetDefault("agent.model", "claude-haiku-5-5")
	v.SetDefault("agent.deep_model", "claude-sonnet-5-5")
	v.SetDefault("agent.chat_model", "")
	v.SetDefault("agent.max_per_day", 50)
	v.SetDefault("agent.investigate_severities", []string{"critical", "warning"})
	v.SetDefault("agent.storm.enabled", true)
	v.SetDefault("agent.storm.threshold", 5)
	v.SetDefault("agent.storm.window", 10*time.Minute)
	v.SetDefault("agent.budget_pause", time.Hour)
	v.SetDefault("agent.price_input_per_mtok", 1.0)
	v.SetDefault("agent.price_output_per_mtok", 5.0)
	v.SetDefault("agent.timeout", 5*time.Minute)
	v.SetDefault("agent.notify_timeout", 4*time.Minute)
	v.SetDefault("agent.max_concurrent", 1)
	v.SetDefault("agent.max_per_hour", 10)
	v.SetDefault("agent.cooldown", 30*time.Minute)
	v.SetDefault("agent.extra_prompt", "")
	v.SetDefault("agent.structured_output", true)

	v.SetDefault("actions.enabled", []string{"k8s.rollout_restart", "k8s.delete_pod", "k8s.scale", "alertmanager.silence", "agent.reinvestigate"})
	v.SetDefault("actions.auto_approve", []string{})
	v.SetDefault("actions.max_auto_per_hour", 2)
	v.SetDefault("actions.allow_reinvestigate_chain", false)
	v.SetDefault("actions.annotation_prefix", "infra-monitor.xonoko.com")
	v.SetDefault("actions.generic.enabled", false)
	v.SetDefault("actions.generic.approved_service_account", "")
	v.SetDefault("actions.generic.exec_enabled", false)
	v.SetDefault("actions.generic.exec_timeout", time.Minute)
	v.SetDefault("actions.gitops.enabled", false)
	v.SetDefault("actions.gitops.repo", "")
	v.SetDefault("actions.gitops.branch", "main")
	v.SetDefault("actions.gitops.token", "")
	v.SetDefault("actions.gitops.token_file", "")
	v.SetDefault("actions.gitops.api_url", "https://api.github.com")
	v.SetDefault("actions.gitops.author_name", "infra-monitor")
	v.SetDefault("actions.gitops.author_email", "infra-monitor.noreply.github.com")
	v.SetDefault("actions.namespace_deny", []string{"kube-system"})
	v.SetDefault("actions.namespace_allow", []string{})
	v.SetDefault("actions.in_cluster", true)
	v.SetDefault("actions.in_cluster_name", "")
	v.SetDefault("actions.proposal_ttl", 7*24*time.Hour)
	v.SetDefault("actions.max_replicas", 10)
	v.SetDefault("actions.max_silence", 24*time.Hour)
	v.SetDefault("actions.timeout", 2*time.Minute)

	v.SetDefault("retention.resolved_incidents", 90*24*time.Hour)
	v.SetDefault("retention.jobs", 7*24*time.Hour)

	v.SetDefault("jobs.workers", 8)
	v.SetDefault("jobs.poll_interval", 2*time.Second)
	v.SetDefault("jobs.lock_timeout", 10*time.Minute)
}

// Load reads configuration. path may be empty: then ./config.yaml, ./config/config.yaml
// and /etc/infra-monitor/config.yaml are tried (a missing file is not an error).
func Load(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	v.SetConfigType("yaml")
	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	} else {
		v.SetConfigName("config")
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
		v.AddConfigPath("/etc/infra-monitor")
		if err := v.ReadInConfig(); err != nil {
			var nf viper.ConfigFileNotFoundError
			if !errors.As(err, &nf) {
				return nil, fmt.Errorf("read config: %w", err)
			}
		}
	}
	cfg := &Config{}
	if err := v.Unmarshal(cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToSliceHookFunc(","),
		durationDaysHook(),
	))); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.resolveSecrets(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// durationDaysHook accepts "90d" style durations in addition to Go durations.
func durationDaysHook() mapstructure.DecodeHookFuncType {
	return func(from, to reflect.Type, data any) (any, error) {
		if from.Kind() != reflect.String || to != reflect.TypeOf(time.Duration(0)) {
			return data, nil
		}
		s := strings.TrimSpace(data.(string))
		if strings.HasSuffix(s, "d") {
			var days int
			if _, err := fmt.Sscanf(s, "%dd", &days); err == nil {
				return time.Duration(days) * 24 * time.Hour, nil
			}
		}
		return time.ParseDuration(s)
	}
}

func readSecret(value *string, file string) error {
	if file == "" {
		return nil
	}
	b, err := os.ReadFile(file) //nolint:gosec // path comes from operator config
	if err != nil {
		return fmt.Errorf("read secret file %s: %w", file, err)
	}
	*value = strings.TrimSpace(string(b))
	return nil
}

func (c *Config) resolveSecrets() error {
	errs := []error{
		readSecret(&c.Database.DSN, c.Database.DSNFile),
		readSecret(&c.Database.Password, c.Database.PasswordFile),
		readSecret(&c.Auth.OIDC.ClientSecret, c.Auth.OIDC.ClientSecretFile),
		readSecret(&c.Heartbeat.PingURL, c.Heartbeat.PingURLFile),
		readSecret(&c.Agent.APIKey, c.Agent.APIKeyFile),
		readSecret(&c.Actions.GitOps.Token, c.Actions.GitOps.TokenFile),
	}
	for i := range c.Sources {
		s := &c.Sources[i]
		errs = append(errs,
			readSecret(&s.Auth.BearerToken, s.Auth.BearerTokenFile),
			readSecret(&s.Auth.BasicPassword, s.Auth.BasicPasswordFile))
	}
	for i := range c.Channels {
		ch := &c.Channels[i]
		errs = append(errs,
			readSecret(&ch.BotToken, ch.BotTokenFile),
			readSecret(&ch.WebhookSecret, ch.WebhookSecretFile))
	}
	return errors.Join(errs...)
}

// Validate checks the configuration and fills per-item defaults.
func (c *Config) Validate() error {
	var errs []error
	if _, err := url.Parse(c.Server.PublicURL); err != nil {
		errs = append(errs, fmt.Errorf("server.public_url: %w", err))
	}
	c.Server.PublicURL = strings.TrimRight(c.Server.PublicURL, "/")
	seen := map[string]bool{}
	for i := range c.Sources {
		s := &c.Sources[i]
		if s.Name == "" {
			errs = append(errs, fmt.Errorf("sources[%d].name is required", i))
		}
		if seen["source:"+s.Name] {
			errs = append(errs, fmt.Errorf("duplicate source %q", s.Name))
		}
		seen["source:"+s.Name] = true
		if s.Type == "" {
			s.Type = "alertmanager"
		}
		if s.Type != "alertmanager" {
			errs = append(errs, fmt.Errorf("source %q: unsupported type %q", s.Name, s.Type))
		}
		if s.ClusterLabel == "" {
			s.ClusterLabel = "cluster"
		}
		if s.NamespaceLabel == "" {
			s.NamespaceLabel = "namespace"
		}
		if s.ReconcileInterval == 0 {
			s.ReconcileInterval = 5 * time.Minute
		}
	}
	for i := range c.Channels {
		ch := &c.Channels[i]
		if ch.Name == "" {
			errs = append(errs, fmt.Errorf("channels[%d].name is required", i))
		}
		if seen["channel:"+ch.Name] {
			errs = append(errs, fmt.Errorf("duplicate channel %q", ch.Name))
		}
		seen["channel:"+ch.Name] = true
		switch ch.Type {
		case "telegram":
			if ch.BotToken == "" {
				errs = append(errs, fmt.Errorf("channel %q: bot_token or bot_token_file is required", ch.Name))
			}
			if ch.ChatID == 0 {
				errs = append(errs, fmt.Errorf("channel %q: chat_id is required", ch.Name))
			}
			if ch.Mode == "" {
				ch.Mode = "polling"
			}
			if ch.Mode != "polling" && ch.Mode != "webhook" {
				errs = append(errs, fmt.Errorf("channel %q: mode must be polling or webhook", ch.Name))
			}
			if ch.Mode == "webhook" && ch.WebhookSecret == "" {
				errs = append(errs, fmt.Errorf("channel %q: webhook mode requires webhook_secret", ch.Name))
			}
			if ch.MessagesPerMinute == 0 {
				ch.MessagesPerMinute = 18
			}
		default:
			errs = append(errs, fmt.Errorf("channel %q: unsupported type %q", ch.Name, ch.Type))
		}
	}
	for i, r := range c.Routing {
		for _, name := range r.Channels {
			if !seen["channel:"+name] {
				errs = append(errs, fmt.Errorf("routing[%d]: unknown channel %q", i, name))
			}
		}
	}
	if c.Auth.OIDC.Enabled {
		o := c.Auth.OIDC
		if o.Issuer == "" || o.ClientID == "" {
			errs = append(errs, errors.New("auth.oidc: issuer and client_id are required"))
		}
		if c.Auth.OIDC.RedirectURL == "" {
			c.Auth.OIDC.RedirectURL = c.Server.PublicURL + "/api/v1/auth/oidc/callback"
		}
	}
	if !c.Auth.Local.Enabled && !c.Auth.OIDC.Enabled {
		errs = append(errs, errors.New("auth: enable at least one of local or oidc"))
	}
	if c.Agent.Enabled && c.Agent.URL == "" {
		errs = append(errs, errors.New("agent.url is required when the agent is enabled"))
	}
	if c.Jobs.Workers < 1 {
		c.Jobs.Workers = 1
	}
	return errors.Join(errs...)
}

// DatabaseURL returns the connection string for pgx.
func (c *DatabaseConfig) DatabaseURL() string {
	if c.DSN != "" {
		return c.DSN
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
		Path:   "/" + c.Name,
	}
	q := url.Values{}
	if c.SSLMode != "" {
		q.Set("sslmode", c.SSLMode)
	}
	if c.SSLRootCert != "" {
		q.Set("sslrootcert", c.SSLRootCert)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// CookieSecure reports whether auth cookies get the Secure flag.
func (c *Config) CookieSecure() bool {
	if c.Auth.CookieSecure != nil {
		return *c.Auth.CookieSecure
	}
	return strings.HasPrefix(c.Server.PublicURL, "https://")
}

// IsDev reports a development environment.
func (c *Config) IsDev() bool { return c.Env == "development" }

// Source returns the named source config.
func (c *Config) Source(name string) (*SourceConfig, bool) {
	for i := range c.Sources {
		if c.Sources[i].Name == name {
			return &c.Sources[i], true
		}
	}
	return nil, false
}
