package main

// Cluster is the subset of the management.loft.sh/v1 Cluster resource this
// controller reads.
type Cluster struct {
	Metadata ObjectMeta  `json:"metadata"`
	Spec     ClusterSpec `json:"spec"`
}

type ClusterSpec struct {
	ArgoCD ArgoCDSpec `json:"argoCD"`
}

// ArgoCDSpec gates whether a Cluster participates in fleet-binding-controller
// reconciliation. Only clusters with argoCD.enabled: true are processed.
type ArgoCDSpec struct {
	Enabled bool `json:"enabled"`
}

type ObjectMeta struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace,omitempty"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

// Application is the management.loft.sh/v1 ArgoCDApplication resource this
// controller creates, patches, and deletes.
type Application struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   ApplicationMeta `json:"metadata"`
	Spec       ApplicationSpec `json:"spec"`
}

type ApplicationMeta struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

type ApplicationSpec struct {
	Destination Destination `json:"destination"`
	TemplateRef TemplateRef `json:"templateRef"`
}

type Destination struct {
	Cluster ClusterRef `json:"cluster"`
}

type ClusterRef struct {
	Name string `json:"name"`
}

type TemplateRef struct {
	Name string `json:"name"`
}

type Selector struct {
	MatchLabels map[string]string `json:"matchLabels"`
}

type ProfileConfig struct {
	Apps []string `json:"apps"`
}
