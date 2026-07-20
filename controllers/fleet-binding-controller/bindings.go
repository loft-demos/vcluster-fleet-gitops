package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	apiGroup                   = "management.loft.sh"
	apiVersion                 = "v1"
	clustersResource           = "clusters"
	argoCDApplicationsResource = "argocdapplications"
	fleetAPIGroup              = "fleet.lab.kurtmadel.com"
	fleetAPIVersion            = "v1alpha1"
	fleetProfilesResource      = "fleetprofiles"
	managedBy                  = "fleet-binding-controller"

	generatedByLabel          = "fleet.lab.kurtmadel.com/generated-by"
	clusterLabel              = "fleet.lab.kurtmadel.com/cluster"
	virtualClusterLabel       = "fleet.lab.kurtmadel.com/virtual-cluster"
	sourceKindLabel           = "fleet.lab.kurtmadel.com/source-kind"
	templateLabel             = "fleet.lab.kurtmadel.com/template"
	profilesAnnotation        = "fleet.lab.kurtmadel.com/profiles"
	dependsOnAnnotation       = "fleet.lab.kurtmadel.com/depends-on"
	dependencyDepthAnnotation = "fleet.lab.kurtmadel.com/dependency-depth"

	// A Cluster annotation with this shape supplies one concrete parameter to
	// the generated ArgoCDApplication whose templateRef matches <template-name>:
	//
	// <template-name>.argocd-template-param.fleet.lab.kurtmadel.com/<parameter-name>
	templateParameterAnnotationSuffix = ".argocd-template-param.fleet.lab.kurtmadel.com"
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

func virtualClusterBindingName(appName, virtualClusterName string) string {
	name := dnsLabel(appName + "-vci-" + virtualClusterName)
	if len(name) <= 63 {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	return strings.TrimRight(name[:54], "-.") + "-" + hex.EncodeToString(digest[:4])
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

type resolvedApplication struct {
	Name      string
	DependsOn []string
	Depth     int
}

type bindingTarget struct {
	Name        string
	Namespace   string
	SourceKind  string
	Destination Destination
	BindingName func(string, string) string
	Labels      map[string]string
}

func indexFleetProfiles(profiles []FleetProfile) map[string]FleetProfile {
	index := make(map[string]FleetProfile, len(profiles))
	for _, profile := range profiles {
		if profile.Metadata.Name != "" {
			index[profile.Metadata.Name] = profile
		}
	}
	return index
}

// resolveApplications combines the selected FleetProfiles, extra apps, and
// skipped apps into a validated dependency graph. Applications retain their
// first-seen ordering, while dependencies from duplicate entries are merged.
func resolveApplications(
	profileNames []string,
	profiles map[string]FleetProfile,
	extraApps []string,
	skipApps []string,
) ([]resolvedApplication, error) {
	applications := map[string]*resolvedApplication{}
	var order []string

	add := func(application FleetProfileApplication) {
		current, ok := applications[application.Name]
		if !ok {
			current = &resolvedApplication{Name: application.Name}
			applications[application.Name] = current
			order = append(order, application.Name)
		}
		current.DependsOn = orderedUnique(append(current.DependsOn, application.DependsOn...))
	}

	for _, profileName := range profileNames {
		profile, ok := profiles[profileName]
		if !ok {
			return nil, fmt.Errorf("unknown FleetProfile %q", profileName)
		}
		for _, application := range profile.Spec.Applications {
			if application.Name == "" {
				return nil, fmt.Errorf("FleetProfile %q contains an application without a name", profileName)
			}
			add(application)
		}
	}
	for _, name := range extraApps {
		add(FleetProfileApplication{Name: name})
	}

	skipped := make(map[string]struct{}, len(skipApps))
	for _, name := range skipApps {
		skipped[name] = struct{}{}
		delete(applications, name)
	}

	filteredOrder := order[:0]
	for _, name := range order {
		if _, skip := skipped[name]; !skip {
			filteredOrder = append(filteredOrder, name)
		}
	}
	order = filteredOrder

	for _, name := range order {
		for _, dependency := range applications[name].DependsOn {
			if _, ok := applications[dependency]; !ok {
				return nil, fmt.Errorf("application %q depends on missing application %q", name, dependency)
			}
		}
	}

	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(applications))
	stack := make([]string, 0, len(applications))
	var calculateDepth func(string) (int, error)
	calculateDepth = func(name string) (int, error) {
		switch state[name] {
		case visiting:
			start := 0
			for i, item := range stack {
				if item == name {
					start = i
					break
				}
			}
			cycle := append(append([]string{}, stack[start:]...), name)
			return 0, fmt.Errorf("application dependency cycle: %s", strings.Join(cycle, " -> "))
		case visited:
			return applications[name].Depth, nil
		}

		state[name] = visiting
		stack = append(stack, name)
		depth := 0
		for _, dependency := range applications[name].DependsOn {
			dependencyDepth, err := calculateDepth(dependency)
			if err != nil {
				return 0, err
			}
			if dependencyDepth+1 > depth {
				depth = dependencyDepth + 1
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = visited
		applications[name].Depth = depth
		return depth, nil
	}

	resolved := make([]resolvedApplication, 0, len(order))
	for _, name := range order {
		if _, err := calculateDepth(name); err != nil {
			return nil, err
		}
		resolved = append(resolved, *applications[name])
	}
	return resolved, nil
}

func applicationReady(application Application) bool {
	if application.Status == nil {
		return false
	}
	status := application.Status.Application
	return status != nil &&
		status.Health.Status == "Healthy" &&
		status.Sync.Status == "Synced"
}

// parametersForTemplate extracts exact ArgoCDApplicationTemplate parameter
// names and string values from the destination Cluster's annotations. Keeping
// the template name in the annotation's DNS prefix leaves the 63-character
// annotation-name segment available for the parameter name.
func parametersForTemplate(annotations map[string]string, templateName string) map[string]interface{} {
	prefix := templateName + templateParameterAnnotationSuffix + "/"
	var parameters map[string]interface{}
	for key, value := range annotations {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		parameterName := strings.TrimPrefix(key, prefix)
		if parameterName == "" {
			continue
		}
		if parameters == nil {
			parameters = map[string]interface{}{}
		}
		parameters[parameterName] = value
	}
	return parameters
}

// desiredBindingsForCluster returns the ArgoCDApplication bindings a Cluster
// should have. A Cluster is only considered when it matches the configured
// selector AND has spec.argoCD.enabled: true.
func desiredBindingsForCluster(
	cluster Cluster,
	cfg *Config,
	profiles map[string]FleetProfile,
	existing map[string]Application,
) ([]Application, error) {
	clusterName := cluster.Metadata.Name
	if clusterName == "" {
		return nil, nil
	}
	if !matchesSelector(cluster, cfg.ClusterSelector) {
		return nil, nil
	}
	if !cluster.Spec.ArgoCD.Enabled {
		return nil, nil
	}

	annotations := cluster.Metadata.Annotations

	profileNames := parseCSV(annotations[cfg.ProfileAnnotation])
	if len(profileNames) == 0 {
		profileNames = cfg.DefaultProfiles
	}

	return desiredBindings(bindingTarget{
		Name:       clusterName,
		Namespace:  cfg.ProjectNamespace,
		SourceKind: "Cluster",
		Destination: Destination{
			Cluster: &ClusterRef{Name: clusterName},
		},
		BindingName: bindingName,
		Labels: map[string]string{
			clusterLabel: clusterName,
		},
	}, annotations, profileNames, cfg, profiles, existing)
}

type privateNodesHelmValues struct {
	PrivateNodes struct {
		Enabled bool `json:"enabled"`
	} `json:"privateNodes"`
}

func virtualClusterPrivateNodes(instance VirtualClusterInstance) (bool, bool, error) {
	if instance.Status.VirtualCluster == nil {
		return false, false, nil
	}
	raw := strings.TrimSpace(instance.Status.VirtualCluster.HelmRelease.Values)
	if raw == "" {
		return false, true, nil
	}
	values := privateNodesHelmValues{}
	if err := yaml.Unmarshal([]byte(raw), &values); err != nil {
		return false, true, fmt.Errorf("parse rendered helm values: %w", err)
	}
	return values.PrivateNodes.Enabled, true, nil
}

func virtualClusterObservabilityDisabled(instance VirtualClusterInstance, cfg *Config) bool {
	value := strings.TrimSpace(instance.Metadata.Annotations[cfg.VCIObservability.DisabledAnnotation])
	disabled, err := strconv.ParseBool(value)
	return err == nil && disabled
}

// desiredBindingsForVirtualClusterInstance automatically selects one of the
// two tenant-observability profiles from the rendered VCI privateNodes shape.
// Explicit profile annotations are additive; the observability-profile
// annotation replaces only the automatically selected observability profile.
func desiredBindingsForVirtualClusterInstance(
	instance VirtualClusterInstance,
	cfg *Config,
	profiles map[string]FleetProfile,
	existing map[string]Application,
) ([]Application, bool, bool, error) {
	if !cfg.VCIObservability.Enabled || instance.Metadata.Name == "" || virtualClusterObservabilityDisabled(instance, cfg) {
		return nil, false, false, nil
	}

	privateNodes, rendered, err := virtualClusterPrivateNodes(instance)
	if err != nil {
		return nil, true, false, err
	}
	if !rendered {
		return nil, true, false, nil
	}

	observabilityProfile := cfg.VCIObservability.SharedNodesProfile
	if privateNodes {
		observabilityProfile = cfg.VCIObservability.PrivateNodesProfile
	}
	if override := strings.TrimSpace(instance.Metadata.Annotations[cfg.VCIObservability.ProfileOverrideAnnotation]); override != "" {
		observabilityProfile = override
	}
	profileNames := orderedUnique(append(
		[]string{observabilityProfile},
		parseCSV(instance.Metadata.Annotations[cfg.ProfileAnnotation])...,
	))

	bindings, err := desiredBindings(bindingTarget{
		Name:       instance.Metadata.Name,
		Namespace:  instance.Metadata.Namespace,
		SourceKind: "VirtualClusterInstance",
		Destination: Destination{
			VirtualCluster: &VirtualClusterRef{Name: instance.Metadata.Name, Target: "vCluster"},
		},
		BindingName: virtualClusterBindingName,
		Labels: map[string]string{
			virtualClusterLabel: instance.Metadata.Name,
		},
	}, instance.Metadata.Annotations, profileNames, cfg, profiles, existing)
	return bindings, true, true, err
}

func desiredBindings(
	target bindingTarget,
	annotations map[string]string,
	profileNames []string,
	cfg *Config,
	profiles map[string]FleetProfile,
	existing map[string]Application,
) ([]Application, error) {
	applications, err := resolveApplications(
		profileNames,
		profiles,
		parseCSV(annotations[cfg.ExtraAppsAnnotation]),
		parseCSV(annotations[cfg.SkipAppsAnnotation]),
	)
	if err != nil {
		return nil, err
	}

	bindings := make([]Application, 0, len(applications))
	for _, application := range applications {
		name := target.BindingName(application.Name, target.Name)
		_, alreadyExists := existing[name]
		if !alreadyExists {
			dependenciesReady := true
			for _, dependency := range application.DependsOn {
				dependencyBinding, ok := existing[target.BindingName(dependency, target.Name)]
				if !ok || !applicationReady(dependencyBinding) {
					dependenciesReady = false
					break
				}
			}
			if !dependenciesReady {
				logDebug(
					"%s %s application %s is waiting for dependencies: %s",
					target.SourceKind,
					target.Name,
					application.Name,
					strings.Join(application.DependsOn, ","),
				)
				continue
			}
		}

		labels := map[string]string{
			"app.kubernetes.io/managed-by": managedBy,
			generatedByLabel:               managedBy,
			sourceKindLabel:                labelValue(target.SourceKind),
			templateLabel:                  labelValue(application.Name),
		}
		for key, value := range target.Labels {
			labels[key] = labelValue(value)
		}
		bindings = append(bindings, Application{
			APIVersion: apiGroup + "/" + apiVersion,
			Kind:       "ArgoCDApplication",
			Metadata: ApplicationMeta{
				Name:      name,
				Namespace: target.Namespace,
				Labels:    labels,
				Annotations: map[string]string{
					profilesAnnotation:        strings.Join(profileNames, ","),
					dependsOnAnnotation:       strings.Join(application.DependsOn, ","),
					dependencyDepthAnnotation: strconv.Itoa(application.Depth),
				},
			},
			Spec: ApplicationSpec{
				Destination: target.Destination,
				TemplateRef: TemplateRef{Name: application.Name},
				Parameters:  parametersForTemplate(annotations, application.Name),
			},
		})
	}
	return bindings, nil
}
