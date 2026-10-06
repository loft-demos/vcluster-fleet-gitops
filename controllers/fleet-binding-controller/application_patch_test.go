package main

import (
	"testing"
)

func liveCollectorApplication() Application {
	return Application{
		Metadata: ApplicationMeta{
			Name:            "shared-node-tenant-collector-vci-stacks-demo",
			Namespace:       "p-default",
			ResourceVersion: "43406648",
			Labels:          map[string]string{generatedByLabel: managedBy, "loft.sh/project": "default"},
			Annotations:     map[string]string{dependsOnAnnotation: "cert-manager"},
		},
		Spec: ApplicationSpec{
			Destination: Destination{VirtualCluster: &VirtualClusterRef{Name: "stacks-demo", Target: "vcluster"}},
			TemplateRef: TemplateRef{Name: "shared-node-tenant-collector"},
			Parameters:  map[string]interface{}{"otlpEndpoint": "https://otel.lab.kurtmadel.com", "replicas": float64(1)},
		},
		Status: &ApplicationStatus{},
	}
}

func TestApplicationNeedsPatch(t *testing.T) {
	tests := map[string]struct {
		mutate func(desired *Application)
		want   bool
	}{
		"unchanged": {mutate: func(*Application) {}},
		"label changed": {
			mutate: func(d *Application) {
				d.Metadata.Labels = map[string]string{generatedByLabel: managedBy, "loft.sh/project": "other"}
			},
			want: true,
		},
		"label only on the live object": {
			// A merge patch never removes labels it does not mention.
			mutate: func(d *Application) { d.Metadata.Labels = map[string]string{generatedByLabel: managedBy} },
		},
		"annotation added": {
			mutate: func(d *Application) {
				d.Metadata.Annotations = map[string]string{dependsOnAnnotation: "cert-manager", "x": "y"}
			},
			want: true,
		},
		"parameter changed": {
			mutate: func(d *Application) {
				d.Spec.Parameters = map[string]interface{}{"otlpEndpoint": "https://other", "replicas": float64(1)}
			},
			want: true,
		},
		"parameters removed": {
			mutate: func(d *Application) { d.Spec.Parameters = nil },
			want:   true,
		},
		"destination switched to a cluster": {
			mutate: func(d *Application) { d.Spec.Destination = Destination{Cluster: &ClusterRef{Name: "loft-cluster"}} },
			want:   true,
		},
		"template changed": {
			mutate: func(d *Application) { d.Spec.TemplateRef = TemplateRef{Name: "other"} },
			want:   true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			appliedApplicationPatches = map[string]appliedApplicationPatch{}
			live := liveCollectorApplication()
			desired := liveCollectorApplication()
			desired.Metadata.ResourceVersion = ""
			desired.Status = nil
			test.mutate(&desired)

			if got := applicationNeedsPatch("p-default/app", live, newApplicationPatch(desired)); got != test.want {
				t.Fatalf("applicationNeedsPatch = %v, want %v", got, test.want)
			}
		})
	}
}

func TestApplicationNeedsPatchIgnoresStatus(t *testing.T) {
	appliedApplicationPatches = map[string]appliedApplicationPatch{}
	live := liveCollectorApplication()
	live.Status = &ApplicationStatus{}
	desired := liveCollectorApplication()
	desired.Status = nil
	if applicationNeedsPatch("p-default/app", live, newApplicationPatch(desired)) {
		t.Fatalf("status differences must not trigger a patch")
	}
}

func TestApplicationNeedsPatchSkipsRepeatOfServerNormalizedPatch(t *testing.T) {
	appliedApplicationPatches = map[string]appliedApplicationPatch{}
	live := liveCollectorApplication()
	// The controller sends an empty target that the server defaults back to
	// "vcluster", so the local merge always predicts a change.
	desired := liveCollectorApplication()
	desired.Spec.Destination.VirtualCluster = &VirtualClusterRef{Name: "stacks-demo"}
	patch := newApplicationPatch(desired)

	if !applicationNeedsPatch("p-default/app", live, patch) {
		t.Fatalf("expected the first pass to patch")
	}
	// The server applied it as a no-op: resourceVersion unchanged.
	rememberApplicationPatch("p-default/app", patch, live.Metadata.ResourceVersion)
	if applicationNeedsPatch("p-default/app", live, patch) {
		t.Fatalf("expected an identical patch on an unchanged object to be skipped")
	}

	// Someone else edits the object: patch again.
	live.Metadata.ResourceVersion = "43409999"
	if !applicationNeedsPatch("p-default/app", live, patch) {
		t.Fatalf("expected a patch after the object changed")
	}

	forgetApplicationPatch("p-default/app")
	if _, ok := appliedApplicationPatches["p-default/app"]; ok {
		t.Fatalf("expected forgetApplicationPatch to drop the entry")
	}
}

func TestMergePatchJSON(t *testing.T) {
	target := map[string]interface{}{
		"a": "keep",
		"b": map[string]interface{}{"c": "old", "d": "keep"},
		"e": "drop",
	}
	patch := map[string]interface{}{
		"b": map[string]interface{}{"c": "new"},
		"e": nil,
		"f": []interface{}{"x"},
	}
	got := mergePatchJSON(target, patch).(map[string]interface{})
	if got["a"] != "keep" || got["b"].(map[string]interface{})["c"] != "new" || got["b"].(map[string]interface{})["d"] != "keep" {
		t.Fatalf("unexpected merge result %v", got)
	}
	if _, ok := got["e"]; ok {
		t.Fatalf("null must delete the key, got %v", got)
	}
	if target["e"] != "drop" {
		t.Fatalf("mergePatchJSON must not modify its input")
	}
}
