// Package poly demonstrates polymorphic JSON unmarshaling: a discriminator
// field selects the concrete Go type for a sibling field at parse time.
package main

import (
	"fmt"

	vjson "github.com/velox-io/json"
	"github.com/velox-io/json/value"
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
	vjson.DefineVariantCases[EventEnvelope, struct {
		_ User    `case:"user"`
		_ Product `case:"product"`
	}]()

	// kindof: descriptor field names are JSON kinds (bool/number/string/
	// array/object). Each field's type is the Go case type for that kind.
	vjson.DefineKindofCases[Response, struct {
		bool   bool
		number float64
		string string
		array  []User
		object User
	}]()

	// Shape is a flat tagged union: the selected case's fields unfold into the
	// host object, so one disc drives several host members at once.
	vjson.DefineVariantCases[Shape, struct {
		_ Circle `case:"circle"`
		_ Rect   `case:"rect"`
	}]()

	// K8sObject carries two independent sibling variants on the same struct,
	// each registered with its own case set via DefineVariantCasesAt (keyed by
	// Go field name):
	//   - Spec (disc "kind"): PodSpec/ServiceSpec.
	//   - Report (disc "observer"): KubeletReport/SchedulerReport.
	vjson.DefineVariantCasesAt[K8sObject, struct {
		_ PodSpec     `case:"Pod"`
		_ ServiceSpec `case:"Service"`
	}]("Spec")
	vjson.DefineVariantCasesAt[K8sObject, struct {
		_ KubeletReport   `case:"kubelet"`
		_ SchedulerReport `case:"scheduler"`
	}]("Report")
	// OwnerReference is itself a variant host (its `owner` field is a sibling
	// variant on `ownerKind`). Demonstrates a second sibling axis via nesting.
	vjson.DefineVariantCases[OwnerReference, struct {
		_ DeploymentOwner `case:"Deployment"`
		_ ReplicaSetOwner `case:"ReplicaSet"`
	}]()

	// NestedInlineHost's inline case carries a second inline variant host, so
	// two discs on two nesting levels resolve in one scan: the outer disc
	// selects the case that brings InnerHost's members, the inner disc selects
	// the case unfolding into InnerHost.
	vjson.DefineVariantCases[NestedInlineHost, struct {
		_ NestedInlineCase `case:"nested"`
	}]()
	vjson.DefineVariantCases[InnerHost, struct {
		_ InnerUser  `case:"user"`
		_ InnerAdmin `case:"admin"`
	}]()

	// NestedRawOuter's only axis is a case whose target is value.Value: the
	// case captures its members raw instead of unfolding them.
	vjson.DefineVariantCases[RawCaseHost, struct {
		_ value.Value `case:"raw"`
	}]()
}

// Response is the kindof host. Data's concrete type is selected by the JSON
// value's kind: bool→bool, number→float64, string→string, array→[]User,
// object→User. No disc sibling; the kind IS the disc, known at the value's
// first token.
type Response struct {
	Data any `json:"data" vjson:"kindof"`
}

// --- flat tagged union (inline variant) ---
//
// Shape's disc and the case's fields live in the same JSON object:
// {"type":"rect","width":3,"height":4} selects Rect and binds width and
// height into it. Inline cases must be structs; a host has at most one
// inline variant.
type Shape struct {
	Type string `json:"type"`
	Body any    `json:",embed" vjson:"variant=type"`
}

type Circle struct {
	Radius float64 `json:"radius"`
}

type Rect struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// --- Kubernetes-style multi-variant example ---
//
// K8sObject mirrors the shape of a Kubernetes API object. The payload already
// sits under its own member (spec), so a plain sibling variant is the right
// tool, not embed. One JSON scan resolves all three axes with no RawMessage:
//
//   - sibling variant on "kind": selects PodSpec/ServiceSpec for spec.
//   - sibling variant on "observer": independent axis with its own disc and
//     case set for report.
//   - nested envelope (OwnerReference) carries a third sibling variant on
//     "ownerKind".

type PodSpec struct {
	Containers []string `json:"containers"`
	NodeName   string   `json:"nodeName"`
}

type ServiceSpec struct {
	Port     int      `json:"port"`
	Selector []string `json:"selector"`
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

// K8sObject is the polymorphic host. Spec (kind axis) and Report (observer
// axis) are sibling variants; Owner is a nested envelope carrying a third.
type K8sObject struct {
	Kind       string         `json:"kind"`
	APIVersion string         `json:"apiVersion"`
	Spec       any            `json:"spec" vjson:"variant=kind"`
	Observer   string         `json:"observer"`
	Report     any            `json:"report" vjson:"variant=observer"`
	Owner      OwnerReference `json:"owner"`
}

// --- nested inline hosts ---
//
// NestedInlineHost is an inline variant host whose case (NestedInlineCase)
// carries a second inline host (InnerHost). Two discs on two nesting levels
// resolve in one scan: the outer one selects the case that brings InnerHost's
// members into the JSON, the inner one selects the case unfolding into it.
type NestedInlineHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

type InnerHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

type NestedInlineCase struct {
	Label string    `json:"label"`
	Inner InnerHost `json:"inner"`
}

// InnerUser / InnerAdmin are InnerHost's cases; their fields unfold into
// InnerHost's JSON object, one nesting level below the outer host.
type InnerUser struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type InnerAdmin struct {
	Level int `json:"level"`
}

// --- raw case host ---
//
// RawCaseHost's case target is value.Value, so the matched members stay a
// navigable Value instead of unfolding into the host. NestedRawOuter binds it
// from an already-parsed Value with UnmarshalValue rather than from bytes.
type RawCaseHost struct {
	Type string `json:"type"`
	Data any    `json:",embed" vjson:"variant=type"`
}

type NestedRawOuter struct {
	Inner RawCaseHost `json:"inner"`
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

	// Flat tagged union: the case's fields unfold into the host object.
	for _, shapeSrc := range []string{
		`{"type":"circle","radius":1.5}`,
		`{"type":"rect","width":3,"height":4}`,
	} {
		var s Shape
		if err := vjson.Unmarshal([]byte(shapeSrc), &s); err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Printf("shape %s -> %T %+v\n", shapeSrc, s.Body, s.Body)
	}

	// Kubernetes-style multi-variant host: one JSON object, three independent
	// polymorphic axes resolved in one scan.
	//   - kind="Pod" selects PodSpec for the spec member
	//   - observer="kubelet" selects KubeletReport for the report member
	//   - owner.ownerKind="Deployment" selects DeploymentOwner inside the
	//     nested OwnerReference envelope
	k8sSrc := `{
		"kind": "Pod",
		"apiVersion": "v1",
		"spec": {"containers": ["nginx", "envoy"], "nodeName": "node-1"},
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

	// Two inline variant levels nested inside each other.
	nestedInlineHosts()

	// Variant case captured as a Value, bound from a parsed document.
	rawCaseFromValue()
}

// nestedInlineHosts binds two nested inline levels in one scan. The outer disc
// selects NestedInlineCase, which brings label and inner into the JSON; inner
// is itself an inline host whose disc selects the case unfolding into it.
func nestedInlineHosts() {
	for _, src := range []string{
		`{"type":"nested","label":"x","inner":{"type":"user","name":"Alice","role":"admin"}}`,
		`{"type":"nested","label":"y","inner":{"type":"admin","level":9}}`,
	} {
		var host NestedInlineHost
		if err := vjson.Unmarshal([]byte(src), &host); err != nil {
			fmt.Println("error:", err)
			return
		}
		outer, ok := host.Data.(NestedInlineCase)
		if !ok {
			fmt.Printf("  unexpected case type %T\n", host.Data)
			return
		}
		fmt.Printf("nested inline: label=%s inner=%T %+v\n", outer.Label, outer.Inner.Data, outer.Inner.Data)
	}
}

// rawCaseFromValue binds with UnmarshalValue, so the source is an
// already-parsed document rather than bytes. The selected case keeps its
// members as a navigable Value.
func rawCaseFromValue() {
	src := `{"inner":{"type":"raw","extra":{"a":1}}}`
	val, err := vjson.Parse([]byte(src))
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	var outer NestedRawOuter
	if err := vjson.UnmarshalValue(val, &outer); err != nil {
		fmt.Println("error:", err)
		return
	}
	raw, ok := outer.Inner.Data.(value.Value)
	if !ok {
		fmt.Printf("  unexpected case type %T\n", outer.Inner.Data)
		return
	}
	typ := raw.Get("type")
	extra := raw.Get("extra")
	a := extra.Get("a")
	typStr, _ := typ.Str()
	aInt, _ := a.Int()
	fmt.Printf("raw case: type=%s extra.a=%d\n", typStr, aInt)
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
// any; type-switch on it.
func useK8sObject(obj K8sObject) {
	fmt.Printf("k8s %s/%s observer=%s\n", obj.APIVersion, obj.Kind, obj.Observer)
	switch s := obj.Spec.(type) {
	case PodSpec:
		fmt.Printf("  spec(pod): containers=%v nodeName=%s\n", s.Containers, s.NodeName)
	case ServiceSpec:
		fmt.Printf("  spec(service): port=%d selector=%v\n", s.Port, s.Selector)
	default:
		fmt.Printf("  unexpected spec type %T\n", s)
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
