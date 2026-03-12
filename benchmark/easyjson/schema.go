// Package easyjson provides isolated easyjson benchmarks.
//
// Types are duplicated here so that easyjson-generated MarshalJSON/UnmarshalJSON
// methods only exist in this package and do not pollute the shared benchmark types.
package easyjson

// --- Tiny: flat struct with basic types ---

type Tiny struct {
	Bool    bool    `json:"bool"`
	Int     int     `json:"int"`
	Int64   int64   `json:"int64"`
	Float64 float64 `json:"float64"`
	String  string  `json:"string"`
}

// --- Small: nested struct with slices (Sonic Book/Author) ---

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

// --- KubePodList: matches testdata/pods.json structure ---

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

// --- EscapeHeavy payload: matches testdata/escape_heavy.json structure ---

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

// --- Twitter: matches testdata/twitter.json structure ---

type TwitterStruct struct {
	Statuses       []Statuses     `json:"statuses"`
	SearchMetadata SearchMetadata `json:"search_metadata"`
}

type Hashtags struct {
	Text    string `json:"text"`
	Indices []int  `json:"indices"`
}

type Entities struct {
	Urls         []interface{} `json:"urls"`
	Hashtags     []Hashtags    `json:"hashtags"`
	UserMentions []interface{} `json:"user_mentions"`
}

type Metadata struct {
	IsoLanguageCode string `json:"iso_language_code"`
	ResultType      string `json:"result_type"`
}

type Urls struct {
	ExpandedURL interface{} `json:"expanded_url"`
	URL         string      `json:"url"`
	Indices     []int       `json:"indices"`
}

type URL struct {
	Urls []Urls `json:"urls"`
}

type Description struct {
	Urls []interface{} `json:"urls"`
}

type UserEntities struct {
	URL         URL         `json:"url"`
	Description Description `json:"description"`
}

type User struct {
	ProfileSidebarFillColor        string       `json:"profile_sidebar_fill_color"`
	ProfileSidebarBorderColor      string       `json:"profile_sidebar_border_color"`
	ProfileBackgroundTile          bool         `json:"profile_background_tile"`
	Name                           string       `json:"name"`
	ProfileImageURL                string       `json:"profile_image_url"`
	CreatedAt                      string       `json:"created_at"`
	Location                       string       `json:"location"`
	FollowRequestSent              interface{}  `json:"follow_request_sent"`
	ProfileLinkColor               string       `json:"profile_link_color"`
	IsTranslator                   bool         `json:"is_translator"`
	IDStr                          string       `json:"id_str"`
	Entities                       UserEntities `json:"entities"`
	DefaultProfile                 bool         `json:"default_profile"`
	ContributorsEnabled            bool         `json:"contributors_enabled"`
	FavouritesCount                int          `json:"favourites_count"`
	URL                            interface{}  `json:"url"`
	ProfileImageURLHTTPS           string       `json:"profile_image_url_https"`
	UtcOffset                      int          `json:"utc_offset"`
	ID                             int          `json:"id"`
	ProfileUseBackgroundImage      bool         `json:"profile_use_background_image"`
	ListedCount                    int          `json:"listed_count"`
	ProfileTextColor               string       `json:"profile_text_color"`
	Lang                           string       `json:"lang"`
	FollowersCount                 int          `json:"followers_count"`
	Protected                      bool         `json:"protected"`
	Notifications                  interface{}  `json:"notifications"`
	ProfileBackgroundImageURLHTTPS string       `json:"profile_background_image_url_https"`
	ProfileBackgroundColor         string       `json:"profile_background_color"`
	Verified                       bool         `json:"verified"`
	GeoEnabled                     bool         `json:"geo_enabled"`
	TimeZone                       string       `json:"time_zone"`
	Description                    string       `json:"description"`
	DefaultProfileImage            bool         `json:"default_profile_image"`
	ProfileBackgroundImageURL      string       `json:"profile_background_image_url"`
	StatusesCount                  int          `json:"statuses_count"`
	FriendsCount                   int          `json:"friends_count"`
	Following                      interface{}  `json:"following"`
	ShowAllInlineMedia             bool         `json:"show_all_inline_media"`
	ScreenName                     string       `json:"screen_name"`
}

type Statuses struct {
	Coordinates          interface{} `json:"coordinates"`
	Favorited            bool        `json:"favorited"`
	Truncated            bool        `json:"truncated"`
	CreatedAt            string      `json:"created_at"`
	IDStr                string      `json:"id_str"`
	Entities             Entities    `json:"entities"`
	InReplyToUserIDStr   interface{} `json:"in_reply_to_user_id_str"`
	Contributors         interface{} `json:"contributors"`
	Text                 string      `json:"text"`
	Metadata             Metadata    `json:"metadata"`
	RetweetCount         int         `json:"retweet_count"`
	InReplyToStatusIDStr interface{} `json:"in_reply_to_status_id_str"`
	ID                   int64       `json:"id"`
	Geo                  interface{} `json:"geo"`
	Retweeted            bool        `json:"retweeted"`
	InReplyToUserID      interface{} `json:"in_reply_to_user_id"`
	Place                interface{} `json:"place"`
	User                 User        `json:"user"`
	InReplyToScreenName  interface{} `json:"in_reply_to_screen_name"`
	Source               string      `json:"source"`
	InReplyToStatusID    interface{} `json:"in_reply_to_status_id"`
}

type SearchMetadata struct {
	MaxID       int64   `json:"max_id"`
	SinceID     int64   `json:"since_id"`
	RefreshURL  string  `json:"refresh_url"`
	NextResults string  `json:"next_results"`
	Count       int     `json:"count"`
	CompletedIn float64 `json:"completed_in"`
	SinceIDStr  string  `json:"since_id_str"`
	Query       string  `json:"query"`
	MaxIDStr    string  `json:"max_id_str"`
}
