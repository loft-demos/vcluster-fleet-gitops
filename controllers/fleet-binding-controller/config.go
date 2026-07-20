package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ProjectNamespace    string
	ClusterSelector     Selector
	DefaultProfiles     []string
	ProfileAnnotation   string
	ExtraAppsAnnotation string
	SkipAppsAnnotation  string
	VCIObservability    VCIObservabilityConfig
	WriterCredentials   WriterCredentialsConfig
}

type VCIObservabilityConfig struct {
	Enabled                   bool
	PrivateNodesProfile       string
	SharedNodesProfile        string
	OTLPEndpoint              string
	DisabledAnnotation        string
	ProfileOverrideAnnotation string
}

type WriterCredentialsConfig struct {
	Enabled            bool
	PlatformURL        string
	PlatformCAPath     string
	InsecureSkipVerify bool
	SecretNamespace    string
	SecretName         string
	InstallerUser      string
	SyncInterval       time.Duration
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}

func getBoolEnv(key string, fallback bool) (bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return parsed, nil
}

// loadJSON parses JSON from value, or from the file at path when set, into out.
// When neither is set, out is left unchanged (its zero/default value).
func loadJSON(value, path string, out interface{}) error {
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, out)
	}
	if value != "" {
		return json.Unmarshal([]byte(value), out)
	}
	return nil
}

func buildConfig() (*Config, error) {
	vciObservabilityEnabled, err := getBoolEnv("FLEET_BINDING_VCI_OBSERVABILITY_ENABLED", false)
	if err != nil {
		return nil, err
	}
	writerCredentialsEnabled, err := getBoolEnv("FLEET_BINDING_WRITER_CREDENTIALS_ENABLED", false)
	if err != nil {
		return nil, err
	}
	platformInsecure, err := getBoolEnv("FLEET_BINDING_PLATFORM_INSECURE_SKIP_VERIFY", false)
	if err != nil {
		return nil, err
	}
	writerSyncInterval, err := parseDuration(getEnv("FLEET_BINDING_WRITER_SYNC_INTERVAL", "10m"))
	if err != nil {
		return nil, fmt.Errorf("parsing writer credential sync interval: %w", err)
	}

	cfg := &Config{
		ProjectNamespace: getEnv("PROJECT_NAMESPACE", "p-platform"),
		ClusterSelector: Selector{
			MatchLabels: map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		},
		DefaultProfiles:     []string{"control-plane-baseline"},
		ProfileAnnotation:   getEnv("FLEET_BINDING_PROFILE_ANNOTATION", "fleet.lab.kurtmadel.com/profiles"),
		ExtraAppsAnnotation: getEnv("FLEET_BINDING_EXTRA_APPS_ANNOTATION", "fleet.lab.kurtmadel.com/extra-apps"),
		SkipAppsAnnotation:  getEnv("FLEET_BINDING_SKIP_APPS_ANNOTATION", "fleet.lab.kurtmadel.com/skip-apps"),
		VCIObservability: VCIObservabilityConfig{
			Enabled:                   vciObservabilityEnabled,
			PrivateNodesProfile:       getEnv("FLEET_BINDING_VCI_PRIVATE_NODES_PROFILE", "tenant-observability-private-nodes"),
			SharedNodesProfile:        getEnv("FLEET_BINDING_VCI_SHARED_NODES_PROFILE", "tenant-observability-shared-nodes"),
			OTLPEndpoint:              getEnv("FLEET_BINDING_VCI_OTLP_ENDPOINT", "https://otel.lab.kurtmadel.com"),
			DisabledAnnotation:        getEnv("FLEET_BINDING_VCI_DISABLED_ANNOTATION", "fleet.lab.kurtmadel.com/observability-disabled"),
			ProfileOverrideAnnotation: getEnv("FLEET_BINDING_VCI_PROFILE_OVERRIDE_ANNOTATION", "fleet.lab.kurtmadel.com/observability-profile"),
		},
		WriterCredentials: WriterCredentialsConfig{
			Enabled:            writerCredentialsEnabled,
			PlatformURL:        strings.TrimRight(os.Getenv("FLEET_BINDING_PLATFORM_URL"), "/"),
			PlatformCAPath:     os.Getenv("FLEET_BINDING_PLATFORM_CA_PATH"),
			InsecureSkipVerify: platformInsecure,
			SecretNamespace:    getEnv("FLEET_BINDING_WRITER_SECRET_NAMESPACE", "observability"),
			SecretName:         getEnv("FLEET_BINDING_WRITER_SECRET_NAME", "otel-otlp-auth"),
			InstallerUser:      getEnv("FLEET_BINDING_INSTALLER_USER", "admin"),
			SyncInterval:       writerSyncInterval,
		},
	}

	if cfg.WriterCredentials.Enabled && !cfg.VCIObservability.Enabled {
		return nil, fmt.Errorf("writer credentials require VCI observability to be enabled")
	}
	if cfg.WriterCredentials.Enabled && cfg.WriterCredentials.PlatformURL == "" {
		return nil, fmt.Errorf("FLEET_BINDING_PLATFORM_URL is required when writer credentials are enabled")
	}

	if err := loadJSON(
		os.Getenv("FLEET_BINDING_CLUSTER_SELECTOR_JSON"),
		os.Getenv("FLEET_BINDING_CLUSTER_SELECTOR_PATH"),
		&cfg.ClusterSelector,
	); err != nil {
		return nil, fmt.Errorf("parsing cluster selector: %w", err)
	}

	if profiles := parseCSV(os.Getenv("FLEET_BINDING_DEFAULT_PROFILES")); len(profiles) > 0 {
		cfg.DefaultProfiles = profiles
	}

	return cfg, nil
}

var durationPattern = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)([smh]?)$`)

func parseDuration(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 30 * time.Second, nil
	}
	match := durationPattern.FindStringSubmatch(value)
	if match == nil {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	amount, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", value, err)
	}
	switch match[2] {
	case "h":
		amount *= 3600
	case "m":
		amount *= 60
	}
	return time.Duration(amount * float64(time.Second)), nil
}
