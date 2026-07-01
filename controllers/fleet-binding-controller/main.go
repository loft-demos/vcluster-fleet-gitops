package main

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"
)

func main() {
	log.SetFlags(log.LstdFlags)
	setLogLevel(getEnv("LOG_LEVEL", "INFO"))

	interval, err := parseDuration(getEnv("RECONCILE_INTERVAL", "30s"))
	if err != nil {
		log.Fatalf("FATAL invalid RECONCILE_INTERVAL: %v", err)
	}

	cfg, err := buildConfig()
	if err != nil {
		log.Fatalf("FATAL invalid configuration: %v", err)
	}

	client, err := NewKubeClient()
	if err != nil {
		log.Fatalf("FATAL failed to initialize kubernetes client: %v", err)
	}

	profileNames := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		profileNames = append(profileNames, name)
	}
	sort.Strings(profileNames)
	logInfo("starting %s for namespace %s with profiles: %s", managedBy, cfg.ProjectNamespace, strings.Join(profileNames, ", "))

	ctx := context.Background()
	for {
		if err := reconcileOnce(ctx, client, cfg); err != nil {
			logError("reconcile failed: %v", err)
		}
		time.Sleep(interval)
	}
}
