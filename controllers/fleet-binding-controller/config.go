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
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
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
	cfg := &Config{
		ProjectNamespace: getEnv("PROJECT_NAMESPACE", "p-platform"),
		ClusterSelector: Selector{
			MatchLabels: map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		},
		DefaultProfiles:     []string{"control-plane-baseline"},
		ProfileAnnotation:   getEnv("FLEET_BINDING_PROFILE_ANNOTATION", "fleet.lab.kurtmadel.com/profiles"),
		ExtraAppsAnnotation: getEnv("FLEET_BINDING_EXTRA_APPS_ANNOTATION", "fleet.lab.kurtmadel.com/extra-apps"),
		SkipAppsAnnotation:  getEnv("FLEET_BINDING_SKIP_APPS_ANNOTATION", "fleet.lab.kurtmadel.com/skip-apps"),
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
