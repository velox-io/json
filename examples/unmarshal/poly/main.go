// Package poly demonstrates polymorphic JSON unmarshaling: a discriminator
// field selects the concrete Go type for a sibling field at parse time.
// See README.md for the full design.
package main

import (
	"fmt"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/vbind"
)

type User struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type Product struct {
	Title string `json:"title"`
	Price int    `json:"price"`
}

// EventEnvelope is the sibling-tag variant host. Type is the discriminator;
// Data is the variant target. The `vjson:"variant=type"` tag names the disc field.
// The descriptor registered in init() maps "user"→User, "product"→Product.
type EventEnvelope struct {
	Type string `json:"type"`
	Data any    `json:"data" vjson:"variant=type"`
}

// JSONVariantCases is the method-form alternative to DefineVariantCases.
// vbind reflects on the parameter type for the case→target mapping but never
// invokes the method. Either form is equivalent; the registry form is used
// below, so this is left out to avoid "both sources" ambiguity. Uncomment
// (and delete the init() registration) to switch.
//
// func (EventEnvelope) JSONVariantCases(struct {
// 	user    User
// 	product Product
// }) {
// }

func init() {
	// Registry-form descriptor: the case value is the field name (or the
	// `case:"..."` tag for blank fields). Each field's type is the target.
	vbind.DefineVariantCases[EventEnvelope, struct {
		_ User    `case:"user"`
		_ Product `case:"product"`
	}]()

	// kindof: descriptor field names are JSON kinds (bool/number/string/
	// array/object). Each field's type is the Go case type for that kind.
	vbind.DefineKindofCases[Response, struct {
		bool   bool
		number float64
		string string
		array  []User
		object User
	}]()

	// K8sObject carries two independent polymorphic axes on the same struct, each
	// registered with its own case set via DefineVariantCasesAt (keyed by Go field
	// name, since the embedded field has no JSON name):
	//   - Object (embedded, disc "kind"): the case is a composite struct
	//     (PodObject/ServiceObject) whose Spec+Status fields unfold into the host.
	//     One disc drives multiple host fields at once.
	//   - Report (sibling, disc "observer"): independent case set (KubeletReport/
	//     SchedulerReport), independent disc.
	// A host permits any number of siblings but at most one embedded variant.
	vbind.DefineVariantCasesAt[K8sObject, struct {
		_ PodObject     `case:"Pod"`
		_ ServiceObject `case:"Service"`
	}]("Object")
	vbind.DefineVariantCasesAt[K8sObject, struct {
		_ KubeletReport   `case:"kubelet"`
		_ SchedulerReport `case:"scheduler"`
	}]("Report")
	// OwnerReference is itself a variant host (its `owner` field is a sibling
	// variant on `ownerKind`). Demonstrates a second sibling axis via nesting.
	vbind.DefineVariantCases[OwnerReference, struct {
		_ DeploymentOwner `case:"Deployment"`
		_ ReplicaSetOwner `case:"ReplicaSet"`
	}]()
}

// Response is the kindof host. Data's concrete type is selected by the JSON
// value's kind: bool→bool, number→float64, string→string, array→[]User,
// object→User. No disc sibling; the kind IS the disc, known at the value's
// first token.
type Response struct {
	Data any `json:"data" vjson:"kindof"`
}

// --- Kubernetes-style multi-variant example ---
//
// K8sObject mirrors the shape of a Kubernetes API object: a Kind/APIVersion
// pair identifies the resource type, and the spec+status payloads are typed
// accordingly. One JSON scan resolves all three axes with no RawMessage:
//
//   - inline variant on "kind": composite case (PodObject{PodSpec, PodStatus})
//     unfolds its fields into the host, so podSpec AND podStatus appear in
//     the host JSON when kind="Pod", both selected by the single disc value.
//   - sibling variant on "observer": independent axis with its own disc and
//     case set. Report stays a real JSON member.
//   - nested envelope (OwnerReference) carries a second sibling variant on
//     "ownerKind".

type PodSpec struct {
	Containers []string `json:"containers"`
	NodeName   string   `json:"nodeName"`
}

type PodStatus struct {
	Phase string `json:"phase"`
	PodIP string `json:"podIP"`
}

type ServiceSpec struct {
	Port     int      `json:"port"`
	Selector []string `json:"selector"`
}

type ServiceStatus struct {
	ClusterIP string `json:"clusterIP"`
}

// PodObject / ServiceObject are the inline variant's composite cases. When
// kind="Pod", PodObject's fields unfold into the host JSON object so the
// host gains podSpec and podStatus members directly.
type PodObject struct {
	PodSpec   PodSpec   `json:"podSpec"`
	PodStatus PodStatus `json:"podStatus"`
}

type ServiceObject struct {
	ServiceSpec   ServiceSpec   `json:"serviceSpec"`
	ServiceStatus ServiceStatus `json:"serviceStatus"`
}

type KubeletReport struct {
	NodeName string `json:"nodeName"`
	HostIP   string `json:"hostIP"`
}

type SchedulerReport struct {
	ScheduledNode string `json:"scheduledNode"`
	SchedulerName string `json:"schedulerName"`
}

// OwnerReference is a nested envelope: itself a variant host, with `owner`
// as a sibling variant on `ownerKind`. Hosts nest to carry additional
// sibling axes.
type OwnerReference struct {
	OwnerKind string `json:"ownerKind"`
	Owner     any    `json:"owner" vjson:"variant=ownerKind"`
}

type DeploymentOwner struct {
	Name string `json:"name"`
}

type ReplicaSetOwner struct {
	Name string `json:"name"`
}

// K8sObject is the polymorphic host. Object is the inline variant (kind axis,
// fields unfold into host); Report is the sibling variant (observer axis, real
// JSON member); Owner is a nested envelope carrying a second sibling.
type K8sObject struct {
	Kind       string         `json:"kind"`
	APIVersion string         `json:"apiVersion"`
	Object     any            `json:",embed" vjson:"variant=kind"`
	Observer   string         `json:"observer"`
	Report     any            `json:"report" vjson:"variant=observer"`
	Owner      OwnerReference `json:"owner"`
}

func main() {
	src := `{"type":"user","data":{"name":"Alice","role":"admin"}}`
	var env EventEnvelope
	if err := vjson.Unmarshal([]byte(src), &env); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("type=%s data=%T %+v\n", env.Type, env.Data, env.Data)

	// Out-of-order: variant value appears before the disc. The parser buffers
	// the variant value and rebinds it at object_close once the disc is known.
	// One JSON scan, no RawMessage.
	srcOOO := `{"data":{"title":"Widget","price":99},"type":"product"}`
	var env2 EventEnvelope
	if err := vjson.Unmarshal([]byte(srcOOO), &env2); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("type=%s data=%T %+v\n", env2.Type, env2.Data, env2.Data)

	// kindof: same envelope parses different JSON value kinds into different
	// Go types. No disc; the JSON value's first token selects the case.
	// Useful for schemaless payloads (e.g. error fields that may be string
	// or object).
	for _, kindofSrc := range []string{
		`{"data":true}`,
		`{"data":42.5}`,
		`{"data":"ok"}`,
		`{"data":[{"name":"Alice","role":"admin"}]}`,
		`{"data":{"name":"Alice","role":"admin"}}`,
		`{"data":null}`,
	} {
		var resp Response
		if err := vjson.Unmarshal([]byte(kindofSrc), &resp); err != nil {
			fmt.Println("error:", err)
			continue
		}
		fmt.Printf("kindof %s -> %T %+v\n", kindofSrc, resp.Data, resp.Data)
		useKindof(resp)
	}

	// Kubernetes-style multi-variant host: one JSON object, three independent
	// polymorphic axes resolved in one scan.
	//   - kind="Pod" unfolds podSpec+podStatus into the host (inline composite
	//     case: one disc drives multiple host fields)
	//   - observer="kubelet" selects KubeletReport for the report member
	//     (sibling variant, independent axis)
	//   - owner.ownerKind="Deployment" selects DeploymentOwner inside the
	//     nested OwnerReference envelope (second sibling, via nesting)
	k8sSrc := `{
		"kind": "Pod",
		"apiVersion": "v1",
		"podSpec": {"containers": ["nginx", "envoy"], "nodeName": "node-1"},
		"podStatus": {"phase": "Running", "podIP": "10.0.0.1"},
		"observer": "kubelet",
		"report": {"nodeName": "node-1", "hostIP": "192.168.1.1"},
		"owner": {"ownerKind": "Deployment", "owner": {"name": "my-deployment"}}
	}`
	var obj K8sObject
	if err := vjson.Unmarshal([]byte(k8sSrc), &obj); err != nil {
		fmt.Println("error:", err)
		return
	}
	useK8sObject(obj)
}

// useKindof consumes a kindof field: type switch on the concrete case the
// parser selected. Standard Go idiom for schemaless payloads. A nil any
// means JSON null was at the field.
func useKindof(resp Response) {
	switch v := resp.Data.(type) {
	case nil:
		fmt.Println("  -> null: nothing to do")
	case bool:
		fmt.Printf("  -> bool branch: enabled=%v\n", v)
	case float64:
		fmt.Printf("  -> number branch: value=%g\n", v)
	case string:
		fmt.Printf("  -> string branch: message=%q\n", v)
	case []User:
		fmt.Printf("  -> array branch: %d users, first=%s\n", len(v), firstOr(v, "<empty>"))
	case User:
		fmt.Printf("  -> object branch: user=%s role=%s\n", v.Name, v.Role)
	default:
		fmt.Printf("  -> unexpected type %T\n", v)
	}
}

func firstOr(users []User, fallback string) string {
	if len(users) == 0 {
		return fallback
	}
	return users[0].Name
}

// useK8sObject consumes a multi-variant host. Each variant field is a separate
// any; type-switch on it. The inline variant's case (PodObject) carries both
// spec and status as struct fields, so one type assertion unpacks everything
// kind drove into the host.
func useK8sObject(obj K8sObject) {
	fmt.Printf("k8s %s/%s observer=%s\n", obj.APIVersion, obj.Kind, obj.Observer)
	switch o := obj.Object.(type) {
	case PodObject:
		fmt.Printf("  spec: containers=%v nodeName=%s\n", o.PodSpec.Containers, o.PodSpec.NodeName)
		fmt.Printf("  status: phase=%s podIP=%s\n", o.PodStatus.Phase, o.PodStatus.PodIP)
	case ServiceObject:
		fmt.Printf("  spec: port=%d selector=%v\n", o.ServiceSpec.Port, o.ServiceSpec.Selector)
		fmt.Printf("  status: clusterIP=%s\n", o.ServiceStatus.ClusterIP)
	default:
		fmt.Printf("  unexpected object type %T\n", o)
	}
	switch r := obj.Report.(type) {
	case KubeletReport:
		fmt.Printf("  report(kubelet): node=%s hostIP=%s\n", r.NodeName, r.HostIP)
	case SchedulerReport:
		fmt.Printf("  report(scheduler): scheduledNode=%s by=%s\n", r.ScheduledNode, r.SchedulerName)
	default:
		fmt.Printf("  unexpected report type %T\n", r)
	}
	switch ow := obj.Owner.Owner.(type) {
	case DeploymentOwner:
		fmt.Printf("  owner(deployment): name=%s\n", ow.Name)
	case ReplicaSetOwner:
		fmt.Printf("  owner(replicaset): name=%s\n", ow.Name)
	default:
		fmt.Printf("  unexpected owner type %T\n", ow)
	}
}
