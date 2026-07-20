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

// VirtualClusterInstance is the subset of the management.loft.sh/v1 resource
// used to classify tenant observability and target generated applications.
type VirtualClusterInstance struct {
	Metadata ObjectMeta                   `json:"metadata"`
	Spec     VirtualClusterInstanceSpec   `json:"spec"`
	Status   VirtualClusterInstanceStatus `json:"status"`
}

type VirtualClusterInstanceSpec struct {
	ClusterRef VirtualClusterClusterRef `json:"clusterRef"`
}

type VirtualClusterClusterRef struct {
	Cluster string `json:"cluster,omitempty"`
}

type VirtualClusterInstanceStatus struct {
	VirtualCluster *VirtualClusterTemplateDefinition `json:"virtualCluster,omitempty"`
}

type VirtualClusterTemplateDefinition struct {
	HelmRelease VirtualClusterHelmRelease `json:"helmRelease,omitempty"`
}

type VirtualClusterHelmRelease struct {
	Values string `json:"values,omitempty"`
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
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Metadata   ApplicationMeta    `json:"metadata"`
	Spec       ApplicationSpec    `json:"spec"`
	Status     *ApplicationStatus `json:"status,omitempty"`
}

type ApplicationMeta struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

type ApplicationSpec struct {
	Destination Destination            `json:"destination"`
	TemplateRef TemplateRef            `json:"templateRef"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

type Destination struct {
	Cluster        *ClusterRef        `json:"cluster,omitempty"`
	VirtualCluster *VirtualClusterRef `json:"virtualCluster,omitempty"`
}

type ClusterRef struct {
	Name string `json:"name"`
}

type VirtualClusterRef struct {
	Name   string `json:"name"`
	Target string `json:"target"`
}

type TemplateRef struct {
	Name string `json:"name"`
}

// ApplicationStatus is the subset of the Platform ArgoCDApplication status
// used to gate dependent bindings. Platform copies the underlying Argo CD
// ApplicationStatus into status.application.
type ApplicationStatus struct {
	Application *ArgoApplicationStatus `json:"application,omitempty"`
}

type ArgoApplicationStatus struct {
	Health ArgoHealthStatus `json:"health,omitempty"`
	Sync   ArgoSyncStatus   `json:"sync,omitempty"`
}

type ArgoHealthStatus struct {
	Status string `json:"status,omitempty"`
}

type ArgoSyncStatus struct {
	Status string `json:"status,omitempty"`
}

type Selector struct {
	MatchLabels map[string]string `json:"matchLabels"`
}

// FleetProfile is a namespaced fleet.lab.kurtmadel.com/v1alpha1 resource that
// defines a dependency-aware set of ArgoCDApplicationTemplate bindings.
type FleetProfile struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   ObjectMeta       `json:"metadata"`
	Spec       FleetProfileSpec `json:"spec"`
}

type FleetProfileSpec struct {
	Applications []FleetProfileApplication `json:"applications"`
}

type FleetProfileApplication struct {
	Name      string   `json:"name"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// AccessKey is the subset of storage.loft.sh/v1 used for controller-managed
// metrics writer and short-lived tenant installer credentials.
type AccessKey struct {
	APIVersion string        `json:"apiVersion,omitempty"`
	Kind       string        `json:"kind,omitempty"`
	Metadata   ObjectMeta    `json:"metadata"`
	Spec       AccessKeySpec `json:"spec"`
}

type AccessKeySpec struct {
	DisplayName string          `json:"displayName,omitempty"`
	Type        string          `json:"type,omitempty"`
	Key         string          `json:"key,omitempty"`
	User        string          `json:"user,omitempty"`
	Subject     string          `json:"subject,omitempty"`
	Groups      []string        `json:"groups,omitempty"`
	TTL         int64           `json:"ttl,omitempty"`
	Scope       *AccessKeyScope `json:"scope,omitempty"`
}

type AccessKeyScope struct {
	Roles           []AccessKeyScopeRole           `json:"roles,omitempty"`
	VirtualClusters []AccessKeyScopeVirtualCluster `json:"virtualClusters,omitempty"`
}

type AccessKeyScopeRole struct {
	Role string `json:"role"`
}

type AccessKeyScopeVirtualCluster struct {
	Project        string `json:"project"`
	VirtualCluster string `json:"virtualCluster"`
}
