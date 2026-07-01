package main

import (
	"regexp"
	"strings"
)

const (
	apiGroup                   = "management.loft.sh"
	apiVersion                 = "v1"
	clustersResource           = "clusters"
	argoCDApplicationsResource = "argocdapplications"
	managedBy                  = "fleet-binding-controller"

	generatedByLabel   = "fleet.lab.kurtmadel.com/generated-by"
	clusterLabel       = "fleet.lab.kurtmadel.com/cluster"
	templateLabel      = "fleet.lab.kurtmadel.com/template"
	profilesAnnotation = "fleet.lab.kurtmadel.com/profiles"
)

func parseCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	var result []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

var dnsLabelInvalidChars = regexp.MustCompile(`[^a-z0-9.-]+`)

func dnsLabel(value string) string {
	value = strings.ToLower(value)
	value = dnsLabelInvalidChars.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-.")
	if value == "" {
		return "binding"
	}
	return value
}

func truncateDNSLabel(value string) string {
	if len(value) > 63 {
		value = value[:63]
	}
	return strings.TrimRight(value, "-")
}

func bindingName(appName, clusterName string) string {
	return truncateDNSLabel(dnsLabel(appName + "-" + clusterName))
}

func labelValue(value string) string {
	return truncateDNSLabel(dnsLabel(value))
}

func matchesSelector(cluster Cluster, selector Selector) bool {
	for key, expected := range selector.MatchLabels {
		if cluster.Metadata.Labels[key] != expected {
			return false
		}
	}
	return true
}

func orderedUnique(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			result = append(result, item)
		}
	}
	return result
}

// desiredBindingsForCluster returns the ArgoCDApplication bindings a Cluster
// should have. A Cluster is only considered when it matches the configured
// selector AND has spec.argoCD.enabled: true.
func desiredBindingsForCluster(cluster Cluster, cfg *Config) []Application {
	clusterName := cluster.Metadata.Name
	if clusterName == "" {
		return nil
	}
	if !matchesSelector(cluster, cfg.ClusterSelector) {
		return nil
	}
	if !cluster.Spec.ArgoCD.Enabled {
		return nil
	}

	annotations := cluster.Metadata.Annotations

	profiles := parseCSV(annotations[cfg.ProfileAnnotation])
	if len(profiles) == 0 {
		profiles = cfg.DefaultProfiles
	}

	var apps []string
	for _, profile := range profiles {
		profileConfig, ok := cfg.Profiles[profile]
		if !ok {
			logWarn("cluster %s references unknown profile %s", clusterName, profile)
			continue
		}
		apps = append(apps, profileConfig.Apps...)
	}

	apps = append(apps, parseCSV(annotations[cfg.ExtraAppsAnnotation])...)

	skipped := make(map[string]struct{})
	for _, app := range parseCSV(annotations[cfg.SkipAppsAnnotation]) {
		skipped[app] = struct{}{}
	}

	apps = orderedUnique(apps)
	filtered := apps[:0]
	for _, app := range apps {
		if _, skip := skipped[app]; !skip {
			filtered = append(filtered, app)
		}
	}

	bindings := make([]Application, 0, len(filtered))
	for _, app := range filtered {
		bindings = append(bindings, Application{
			APIVersion: apiGroup + "/" + apiVersion,
			Kind:       "ArgoCDApplication",
			Metadata: ApplicationMeta{
				Name:      bindingName(app, clusterName),
				Namespace: cfg.ProjectNamespace,
				Labels: map[string]string{
					"app.kubernetes.io/managed-by": managedBy,
					generatedByLabel:               managedBy,
					clusterLabel:                   labelValue(clusterName),
					templateLabel:                  labelValue(app),
				},
				Annotations: map[string]string{
					profilesAnnotation: strings.Join(profiles, ","),
				},
			},
			Spec: ApplicationSpec{
				Destination: Destination{Cluster: ClusterRef{Name: clusterName}},
				TemplateRef: TemplateRef{Name: app},
			},
		})
	}
	return bindings
}
