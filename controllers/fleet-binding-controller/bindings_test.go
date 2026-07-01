package main

import (
	"reflect"
	"testing"
)

var testConfig = &Config{
	ProjectNamespace: "p-platform",
	Profiles: map[string]ProfileConfig{
		"control-plane-baseline":          {Apps: []string{"cert-manager", "metrics-server", "metallb"}},
		"vcp-management-cluster-baseline": {Apps: []string{"cert-manager", "metrics-server", "vcluster-gitops-watcher"}},
		"gpu-nvidia-baseline":             {Apps: []string{"nvidia-gpu-operator", "nvidia-dra-driver-gpu"}},
	},
	ClusterSelector: Selector{
		MatchLabels: map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
	},
	DefaultProfiles:     []string{"control-plane-baseline"},
	ProfileAnnotation:   "fleet.lab.kurtmadel.com/profiles",
	ExtraAppsAnnotation: "fleet.lab.kurtmadel.com/extra-apps",
	SkipAppsAnnotation:  "fleet.lab.kurtmadel.com/skip-apps",
}

func testCluster(name string, labels, annotations map[string]string, argoCDEnabled bool) Cluster {
	return Cluster{
		Metadata: ObjectMeta{Name: name, Labels: labels, Annotations: annotations},
		Spec:     ClusterSpec{ArgoCD: ArgoCDSpec{Enabled: argoCDEnabled}},
	}
}

func templateNames(bindings []Application) []string {
	names := make([]string, len(bindings))
	for i, binding := range bindings {
		names[i] = binding.Spec.TemplateRef.Name
	}
	return names
}

func TestDefaultProfileIsUsedForSelectedCluster(t *testing.T) {
	cluster := testCluster(
		"cp-blacksburg-dc1",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		nil,
		true,
	)

	got := templateNames(desiredBindingsForCluster(cluster, testConfig))
	want := []string{"cert-manager", "metrics-server", "metallb"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestProfileAnnotationCanCombineProfiles(t *testing.T) {
	cluster := testCluster(
		"gpu-cluster",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		map[string]string{
			"fleet.lab.kurtmadel.com/profiles": "control-plane-baseline,gpu-nvidia-baseline",
		},
		true,
	)

	got := templateNames(desiredBindingsForCluster(cluster, testConfig))
	want := []string{
		"cert-manager", "metrics-server", "metallb",
		"nvidia-gpu-operator", "nvidia-dra-driver-gpu",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestExtraAndSkipAnnotationsAdjustApps(t *testing.T) {
	cluster := testCluster(
		"edge",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		map[string]string{
			"fleet.lab.kurtmadel.com/extra-apps": "custom-app",
			"fleet.lab.kurtmadel.com/skip-apps":  "metallb",
		},
		true,
	)

	got := templateNames(desiredBindingsForCluster(cluster, testConfig))
	want := []string{"cert-manager", "metrics-server", "custom-app"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestUnselectedClusterGetsNoBindings(t *testing.T) {
	cluster := testCluster("other", nil, nil, true)

	bindings := desiredBindingsForCluster(cluster, testConfig)
	if len(bindings) != 0 {
		t.Fatalf("expected no bindings, got %v", bindings)
	}
}

func TestClusterWithArgoCDDisabledGetsNoBindings(t *testing.T) {
	cluster := testCluster(
		"cp-blacksburg-dc1",
		map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		nil,
		false,
	)

	bindings := desiredBindingsForCluster(cluster, testConfig)
	if len(bindings) != 0 {
		t.Fatalf("expected no bindings when spec.argoCD.enabled is false, got %v", bindings)
	}
}

func TestClusterWithoutArgoCDSpecGetsNoBindings(t *testing.T) {
	cluster := Cluster{
		Metadata: ObjectMeta{
			Name:   "cp-blacksburg-dc1",
			Labels: map[string]string{"fleet.lab.kurtmadel.com/baseline": "true"},
		},
	}

	bindings := desiredBindingsForCluster(cluster, testConfig)
	if len(bindings) != 0 {
		t.Fatalf("expected no bindings when spec.argoCD is unset, got %v", bindings)
	}
}
