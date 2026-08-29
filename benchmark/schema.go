package benchmark

// =============================================================================
// Struct Types
// =============================================================================

// --- Tiny: flat struct with basic types ---

type Tiny struct {
	Bool    bool    `json:"bool"`
	Int     int     `json:"int"`
	Int64   int64   `json:"int64"`
	Float64 float64 `json:"float64"`
	String  string  `json:"string"`
}

// --- Small: Sonic-style small struct with nested types and slices ---

type Book struct {
	BookId  int       `json:"id"`
	BookIds []int     `json:"ids"`
	Title   string    `json:"title"`
	Titles  []string  `json:"titles"`
	Price   float64   `json:"price"`
	Prices  []float64 `json:"prices"`
	Hot     bool      `json:"hot"`
	Hots    []bool    `json:"hots"`
	Author  Author    `json:"author"`
	Authors []Author  `json:"authors"`
	Weights []int     `json:"weights"`
}

type Author struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
	Male bool   `json:"male"`
}

// --- pods: matches the corpus kubepods dataset structure ---

type KubePodList struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Items      []KubePod    `json:"items"`
	Metadata   ListMetadata `json:"metadata"`
}

type ListMetadata struct {
	ResourceVersion string `json:"resourceVersion"`
}

type KubePod struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Metadata   PodMeta   `json:"metadata"`
	Spec       PodSpec   `json:"spec"`
	Status     PodStatus `json:"status"`
}

type PodMeta struct {
	Annotations       map[string]string `json:"annotations"`
	CreationTimestamp string            `json:"creationTimestamp"`
	GenerateName      string            `json:"generateName"`
	Labels            map[string]string `json:"labels"`
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	OwnerReferences   []OwnerReference  `json:"ownerReferences"`
	ResourceVersion   string            `json:"resourceVersion"`
	UID               string            `json:"uid"`
}

type OwnerReference struct {
	APIVersion         string `json:"apiVersion"`
	BlockOwnerDeletion bool   `json:"blockOwnerDeletion"`
	Controller         bool   `json:"controller"`
	Kind               string `json:"kind"`
	Name               string `json:"name"`
	UID                string `json:"uid"`
}

type PodSpec struct {
	Affinity                      *Affinity    `json:"affinity"`
	Containers                    []Container  `json:"containers"`
	DNSPolicy                     string       `json:"dnsPolicy"`
	EnableServiceLinks            bool         `json:"enableServiceLinks"`
	HostNetwork                   bool         `json:"hostNetwork"`
	NodeName                      string       `json:"nodeName"`
	PreemptionPolicy              string       `json:"preemptionPolicy"`
	Priority                      int64        `json:"priority"`
	PriorityClassName             string       `json:"priorityClassName"`
	RestartPolicy                 string       `json:"restartPolicy"`
	SchedulerName                 string       `json:"schedulerName"`
	SecurityContext               PodSecCtx    `json:"securityContext"`
	ServiceAccount                string       `json:"serviceAccount"`
	ServiceAccountName            string       `json:"serviceAccountName"`
	TerminationGracePeriodSeconds int64        `json:"terminationGracePeriodSeconds"`
	Tolerations                   []Toleration `json:"tolerations"`
	Volumes                       []Volume     `json:"volumes"`
}

type Affinity struct {
	NodeAffinity *NodeAffinity `json:"nodeAffinity"`
}

type NodeAffinity struct {
	RequiredDuringSchedulingIgnoredDuringExecution *NodeSelector `json:"requiredDuringSchedulingIgnoredDuringExecution"`
}

type NodeSelector struct {
	NodeSelectorTerms []NodeSelectorTerm `json:"nodeSelectorTerms"`
}

type NodeSelectorTerm struct {
	MatchFields []NodeSelectorRequirement `json:"matchFields"`
}

type NodeSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

type Container struct {
	Args                     []string      `json:"args"`
	Command                  []string      `json:"command"`
	Env                      []EnvVar      `json:"env"`
	Image                    string        `json:"image"`
	ImagePullPolicy          string        `json:"imagePullPolicy"`
	Name                     string        `json:"name"`
	Resources                ContainerRes  `json:"resources"`
	SecurityContext          *ContainerSec `json:"securityContext"`
	TerminationMessagePath   string        `json:"terminationMessagePath"`
	TerminationMessagePolicy string        `json:"terminationMessagePolicy"`
	VolumeMounts             []VolumeMount `json:"volumeMounts"`
}

type ContainerRes struct{}

type ContainerSec struct {
	Privileged bool `json:"privileged"`
}

type EnvVar struct {
	Name      string        `json:"name"`
	ValueFrom *EnvVarSource `json:"valueFrom"`
}

type EnvVarSource struct {
	FieldRef *ObjectFieldSelector `json:"fieldRef"`
}

type ObjectFieldSelector struct {
	APIVersion string `json:"apiVersion"`
	FieldPath  string `json:"fieldPath"`
}

type VolumeMount struct {
	MountPath string `json:"mountPath"`
	Name      string `json:"name"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

type PodSecCtx struct{}

type Toleration struct {
	Effect   string `json:"effect,omitempty"`
	Key      string `json:"key,omitempty"`
	Operator string `json:"operator"`
}

type Volume struct {
	Name      string              `json:"name"`
	HostPath  *HostPathVolSource  `json:"hostPath,omitempty"`
	ConfigMap *ConfigMapVolSource `json:"configMap,omitempty"`
	Projected *ProjectedVolSource `json:"projected,omitempty"`
}

type HostPathVolSource struct {
	Path string `json:"path"`
	Type string `json:"type"`
}

type ConfigMapVolSource struct {
	DefaultMode int         `json:"defaultMode"`
	Name        string      `json:"name"`
	Items       []KeyToPath `json:"items,omitempty"`
}

type KeyToPath struct {
	Key  string `json:"key"`
	Path string `json:"path"`
}

type ProjectedVolSource struct {
	DefaultMode int                `json:"defaultMode"`
	Sources     []VolumeProjection `json:"sources"`
}

type VolumeProjection struct {
	ServiceAccountToken *SATokenProjection     `json:"serviceAccountToken,omitempty"`
	ConfigMap           *ConfigMapVolSource    `json:"configMap,omitempty"`
	DownwardAPI         *DownwardAPIProjection `json:"downwardAPI,omitempty"`
}

type SATokenProjection struct {
	ExpirationSeconds int64  `json:"expirationSeconds"`
	Path              string `json:"path"`
}

type DownwardAPIProjection struct {
	Items []DownwardAPIVolumeFile `json:"items"`
}

type DownwardAPIVolumeFile struct {
	FieldRef *ObjectFieldSelector `json:"fieldRef"`
	Path     string               `json:"path"`
}

type PodStatus struct {
	Conditions        []PodCondition    `json:"conditions"`
	ContainerStatuses []ContainerStatus `json:"containerStatuses"`
	HostIP            string            `json:"hostIP"`
	Phase             string            `json:"phase"`
	PodIP             string            `json:"podIP"`
	PodIPs            []PodIP           `json:"podIPs"`
	QOSClass          string            `json:"qosClass"`
	StartTime         string            `json:"startTime"`
}

type PodCondition struct {
	LastProbeTime      string `json:"lastProbeTime"`
	LastTransitionTime string `json:"lastTransitionTime"`
	Status             string `json:"status"`
	Type               string `json:"type"`
}

type ContainerStatus struct {
	ContainerID  string         `json:"containerID"`
	Image        string         `json:"image"`
	ImageID      string         `json:"imageID"`
	LastState    ContainerState `json:"lastState"`
	Name         string         `json:"name"`
	Ready        bool           `json:"ready"`
	RestartCount int            `json:"restartCount"`
	Started      bool           `json:"started"`
	State        ContainerState `json:"state"`
}

type ContainerState struct {
	Running *ContainerStateRunning `json:"running,omitempty"`
}

type ContainerStateRunning struct {
	StartedAt string `json:"startedAt"`
}

type PodIP struct {
	IP string `json:"ip"`
}

// --- EscapeHeavy payload: matches the corpus escape_heavy dataset structure ---

type NetAddr struct {
	IP   string `json:"ip"`
	Host string `json:"host"`
}

type Pod struct {
	Name           string  `json:"name"`
	FQDN           string  `json:"fqdn"`
	ClusterNetAddr NetAddr `json:"clusterNetAddr"`
}

type PodFull struct {
	Name            string   `json:"name"`
	FQDN            string   `json:"fqdn"`
	Region          string   `json:"region"`
	Zone            string   `json:"zone"`
	ClusterNetAddr  NetAddr  `json:"clusterNetAddr"`
	ExternalNetAddr *NetAddr `json:"externalNetAddr"`
	Misc            string   `json:"misc"`
}

type Resources struct {
	CPU    int `json:"cpu"`
	Memory int `json:"memory"`
}

type Component struct {
	Replicas  int       `json:"replicas"`
	Shards    int       `json:"shards"`
	Resources Resources `json:"resources"`
	Pods      []Pod     `json:"pods"`
}

type Components struct {
	Proxy Component `json:"proxy"`
	Redis Component `json:"redis"`
}

type Cluster struct {
	Name       string     `json:"name"`
	Components Components `json:"components"`
	Tenant     string     `json:"tenant"`
}

type EscapeHeavyPayload struct {
	Params  string  `json:"params"`
	Pod     PodFull `json:"pod"`
	Cluster Cluster `json:"cluster"`
}

// --- SpikyPayload: variable-size struct for spike prediction benchmarks ---
// By varying len(Items) and string field lengths, the same type produces
// JSON from ~300 bytes (small) to multi-MB (spike).

type SpikyItem struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Payload string `json:"payload"`
}

type SpikyPayload struct {
	Kind  string      `json:"kind"`
	Seq   int         `json:"seq"`
	Items []SpikyItem `json:"items"`
}

// --- LogRecord: matches the corpus log.json.zst stream (OTEL-style log lines) ---

type LogResource struct {
	Name   string `json:"name"`
	Module string `json:"module"`
}

type LogRecord struct {
	SeverityText   string            `json:"SeverityText"`
	Timestamp      string            `json:"Timestamp"`
	Caller         string            `json:"caller"`
	Message        string            `json:"Message"`
	Resource       LogResource       `json:"Resource"`
	SeverityNumber int               `json:"SeverityNumber"`
	Attributes     map[string]string `json:"Attributes"`
}

// --- MediumPayload: matches the corpus medium dataset structure ---
// FullContact-style person-enrichment record. Fields observed as null in the
// sample use pointer types so JSON null survives a round-trip distinctly
// from the zero value.

type MediumPayload struct {
	Person  *MediumPerson  `json:"person"`
	Company *MediumCompany `json:"company"`
}

type MediumPerson struct {
	ID         string           `json:"id"`
	Name       MediumPersonName `json:"name"`
	Email      string           `json:"email"`
	Gender     string           `json:"gender"`
	Location   string           `json:"location"`
	Geo        MediumGeo        `json:"geo"`
	Bio        string           `json:"bio"`
	Site       string           `json:"site"`
	Avatar     string           `json:"avatar"`
	Employment MediumEmployment `json:"employment"`
	Facebook   MediumFacebook   `json:"facebook"`
	GitHub     MediumGitHub     `json:"github"`
	Twitter    MediumTwitter    `json:"twitter"`
	LinkedIn   MediumLinkedIn   `json:"linkedin"`
	GooglePlus MediumGooglePlus `json:"googleplus"`
	AngelList  MediumAngelList  `json:"angellist"`
	Klout      MediumKlout      `json:"klout"`
	Foursquare MediumFoursquare `json:"foursquare"`
	AboutMe    MediumAboutMe    `json:"aboutme"`
	Gravatar   MediumGravatar   `json:"gravatar"`
	Fuzzy      bool             `json:"fuzzy"`
}

type MediumPersonName struct {
	FullName   string `json:"fullName"`
	GivenName  string `json:"givenName"`
	FamilyName string `json:"familyName"`
}

type MediumGeo struct {
	City    string  `json:"city"`
	State   string  `json:"state"`
	Country string  `json:"country"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
}

type MediumEmployment struct {
	Name   string `json:"name"`
	Title  string `json:"title"`
	Domain string `json:"domain"`
}

type MediumFacebook struct {
	Handle string `json:"handle"`
}

type MediumGitHub struct {
	Handle    string `json:"handle"`
	ID        int64  `json:"id"`
	Avatar    string `json:"avatar"`
	Company   string `json:"company"`
	Blog      string `json:"blog"`
	Followers int    `json:"followers"`
	Following int    `json:"following"`
}

type MediumTwitter struct {
	Handle    string  `json:"handle"`
	ID        int64   `json:"id"`
	Bio       *string `json:"bio"`
	Followers int     `json:"followers"`
	Following int     `json:"following"`
	Statuses  int     `json:"statuses"`
	Favorites int     `json:"favorites"`
	Location  string  `json:"location"`
	Site      string  `json:"site"`
	Avatar    *string `json:"avatar"`
}

type MediumLinkedIn struct {
	Handle string `json:"handle"`
}

type MediumGooglePlus struct {
	Handle *string `json:"handle"`
}

type MediumAngelList struct {
	Handle    string `json:"handle"`
	ID        int64  `json:"id"`
	Bio       string `json:"bio"`
	Blog      string `json:"blog"`
	Site      string `json:"site"`
	Followers int    `json:"followers"`
	Avatar    string `json:"avatar"`
}

type MediumKlout struct {
	Handle *string  `json:"handle"`
	Score  *float64 `json:"score"`
}

type MediumFoursquare struct {
	Handle *string `json:"handle"`
}

type MediumAboutMe struct {
	Handle string  `json:"handle"`
	Bio    *string `json:"bio"`
	Avatar *string `json:"avatar"`
}

type MediumGravatar struct {
	Handle  string                 `json:"handle"`
	URLs    []string               `json:"urls"`
	Avatar  string                 `json:"avatar"`
	Avatars []MediumGravatarAvatar `json:"avatars"`
}

type MediumGravatarAvatar struct {
	URL  string `json:"url"`
	Type string `json:"type"`
}

// MediumCompany is a placeholder: the sample payload has company=null, so the
// concrete fields are not yet known. Add them when a non-null sample appears.
type MediumCompany struct{}
