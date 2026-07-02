package main

import (
	"context"
	"log"
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

	logInfo("starting %s for namespace %s", managedBy, cfg.ProjectNamespace)

	ctx := context.Background()
	for {
		if err := reconcileOnce(ctx, client, cfg); err != nil {
			logError("reconcile failed: %v", err)
		}
		time.Sleep(interval)
	}
}
