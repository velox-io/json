//go:build bincodec

package benchmark

import (
	"encoding/json"
	"sync"
	"testing"

	"buf.build/go/hyperpb"
	kubepods "dev.local/benchmark/pb/kubepods"
	"dev.local/benchmark/vtproto"
	"github.com/apache/fory/go/fory"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// foryBenchWireOnce guards the one-time conversion of KubePodsJSON into
// Fory wire bytes for each Fory configuration. The wire bytes are reused
// across all unmarshal iterations.
var (
	foryXlangWireOnce  sync.Once
	foryXlangWireData  []byte
	foryNativeWireOnce sync.Once
	foryNativeWireData []byte

	foryXlangValueOnce  sync.Once
	foryXlangValue      KubePodList
	foryNativeValueOnce sync.Once
	foryNativeValue     KubePodList

	hyperpbWireOnce sync.Once
	hyperpbWireData []byte

	hyperpbMsgTypeOnce sync.Once
	hyperpbMsgType     *hyperpb.MessageType
)

// KubePods Fory type IDs. User type IDs start above fory's reserved range.
const (
	foryTypeIDKubePodList = 100 + iota
	foryTypeIDListMetadata
	foryTypeIDKubePod
	foryTypeIDPodMeta
	foryTypeIDOwnerReference
	foryTypeIDPodSpec
	foryTypeIDAffinity
	foryTypeIDNodeAffinity
	foryTypeIDNodeSelector
	foryTypeIDNodeSelectorTerm
	foryTypeIDNodeSelectorRequirement
	foryTypeIDContainer
	foryTypeIDContainerRes
	foryTypeIDContainerSec
	foryTypeIDEnvVar
	foryTypeIDEnvVarSource
	foryTypeIDObjectFieldSelector
	foryTypeIDVolumeMount
	foryTypeIDPodSecCtx
	foryTypeIDToleration
	foryTypeIDVolume
	foryTypeIDHostPathVolSource
	foryTypeIDConfigMapVolSource
	foryTypeIDKeyToPath
	foryTypeIDProjectedVolSource
	foryTypeIDVolumeProjection
	foryTypeIDSATokenProjection
	foryTypeIDDownwardAPIProjection
	foryTypeIDDownwardAPIVolumeFile
	foryTypeIDPodStatus
	foryTypeIDPodCondition
	foryTypeIDContainerStatus
	foryTypeIDContainerState
	foryTypeIDContainerStateRunning
	foryTypeIDPodIP
)

// registerKubePodsTypes registers every struct type in the KubePodList graph.
// Fory requires all nested struct types to be registered before serialization.
func registerKubePodsTypes(f *fory.Fory) {
	must := func(err error) {
		if err != nil {
			panic("fory register: " + err.Error())
		}
	}
	must(f.RegisterStruct(KubePodList{}, foryTypeIDKubePodList))
	must(f.RegisterStruct(ListMetadata{}, foryTypeIDListMetadata))
	must(f.RegisterStruct(KubePod{}, foryTypeIDKubePod))
	must(f.RegisterStruct(PodMeta{}, foryTypeIDPodMeta))
	must(f.RegisterStruct(OwnerReference{}, foryTypeIDOwnerReference))
	must(f.RegisterStruct(PodSpec{}, foryTypeIDPodSpec))
	must(f.RegisterStruct(Affinity{}, foryTypeIDAffinity))
	must(f.RegisterStruct(NodeAffinity{}, foryTypeIDNodeAffinity))
	must(f.RegisterStruct(NodeSelector{}, foryTypeIDNodeSelector))
	must(f.RegisterStruct(NodeSelectorTerm{}, foryTypeIDNodeSelectorTerm))
	must(f.RegisterStruct(NodeSelectorRequirement{}, foryTypeIDNodeSelectorRequirement))
	must(f.RegisterStruct(Container{}, foryTypeIDContainer))
	must(f.RegisterStruct(ContainerRes{}, foryTypeIDContainerRes))
	must(f.RegisterStruct(ContainerSec{}, foryTypeIDContainerSec))
	must(f.RegisterStruct(EnvVar{}, foryTypeIDEnvVar))
	must(f.RegisterStruct(EnvVarSource{}, foryTypeIDEnvVarSource))
	must(f.RegisterStruct(ObjectFieldSelector{}, foryTypeIDObjectFieldSelector))
	must(f.RegisterStruct(VolumeMount{}, foryTypeIDVolumeMount))
	must(f.RegisterStruct(PodSecCtx{}, foryTypeIDPodSecCtx))
	must(f.RegisterStruct(Toleration{}, foryTypeIDToleration))
	must(f.RegisterStruct(Volume{}, foryTypeIDVolume))
	must(f.RegisterStruct(HostPathVolSource{}, foryTypeIDHostPathVolSource))
	must(f.RegisterStruct(ConfigMapVolSource{}, foryTypeIDConfigMapVolSource))
	must(f.RegisterStruct(KeyToPath{}, foryTypeIDKeyToPath))
	must(f.RegisterStruct(ProjectedVolSource{}, foryTypeIDProjectedVolSource))
	must(f.RegisterStruct(VolumeProjection{}, foryTypeIDVolumeProjection))
	must(f.RegisterStruct(SATokenProjection{}, foryTypeIDSATokenProjection))
	must(f.RegisterStruct(DownwardAPIProjection{}, foryTypeIDDownwardAPIProjection))
	must(f.RegisterStruct(DownwardAPIVolumeFile{}, foryTypeIDDownwardAPIVolumeFile))
	must(f.RegisterStruct(PodStatus{}, foryTypeIDPodStatus))
	must(f.RegisterStruct(PodCondition{}, foryTypeIDPodCondition))
	must(f.RegisterStruct(ContainerStatus{}, foryTypeIDContainerStatus))
	must(f.RegisterStruct(ContainerState{}, foryTypeIDContainerState))
	must(f.RegisterStruct(ContainerStateRunning{}, foryTypeIDContainerStateRunning))
	must(f.RegisterStruct(PodIP{}, foryTypeIDPodIP))
}

// newForyXlang creates a Fory instance configured for cross-language
// interoperability (the default mode). This is the fair comparison point
// against protobuf, which is also a cross-language format.
func newForyXlang() *fory.Fory {
	f := fory.New(fory.WithXlang(true))
	registerKubePodsTypes(f)
	return f
}

// newForyNative creates a Fory instance configured for Go-only payloads
// with schema-evolution compatibility disabled. This is the fastest Fory
// mode and shows the upper bound of Fory performance when cross-language
// wire compatibility is not required.
func newForyNative() *fory.Fory {
	f := fory.New(fory.WithXlang(false), fory.WithCompatible(false))
	registerKubePodsTypes(f)
	return f
}

// loadKubePodsForyXlangWire decodes KubePodsJSON into KubePodList and
// marshals it with the xlang Fory instance, caching the wire bytes.
func loadKubePodsForyXlangWire() []byte {
	foryXlangWireOnce.Do(func() {
		var v KubePodList
		if err := json.Unmarshal(KubePodsJSON, &v); err != nil {
			panic("fory xlang: decode pods json: " + err.Error())
		}
		f := newForyXlang()
		wire, err := f.Serialize(&v)
		if err != nil {
			panic("fory xlang: marshal pods: " + err.Error())
		}
		// Clone the view; the slice is backed by Fory's internal buffer
		// and would be invalidated on the next Serialize call.
		foryXlangWireData = append([]byte(nil), wire...)
	})
	return foryXlangWireData
}

func loadKubePodsForyNativeWire() []byte {
	foryNativeWireOnce.Do(func() {
		var v KubePodList
		if err := json.Unmarshal(KubePodsJSON, &v); err != nil {
			panic("fory native: decode pods json: " + err.Error())
		}
		f := newForyNative()
		wire, err := f.Serialize(&v)
		if err != nil {
			panic("fory native: marshal pods: " + err.Error())
		}
		foryNativeWireData = append([]byte(nil), wire...)
	})
	return foryNativeWireData
}

// loadKubePodsForyXlangValue decodes KubePodsJSON into KubePodList for
// marshal benchmarks (xlang mode reuses the same value).
func loadKubePodsForyXlangValue() *KubePodList {
	foryXlangValueOnce.Do(func() {
		if err := json.Unmarshal(KubePodsJSON, &foryXlangValue); err != nil {
			panic("fory xlang: decode pods json: " + err.Error())
		}
	})
	return &foryXlangValue
}

func loadKubePodsForyNativeValue() *KubePodList {
	foryNativeValueOnce.Do(func() {
		if err := json.Unmarshal(KubePodsJSON, &foryNativeValue); err != nil {
			panic("fory native: decode pods json: " + err.Error())
		}
	})
	return &foryNativeValue
}

func loadKubePodsHyperPBWire() []byte {
	hyperpbWireOnce.Do(func() {
		var msg kubepods.KubePodList
		opts := protojson.UnmarshalOptions{DiscardUnknown: true}
		if err := opts.Unmarshal(KubePodsJSON, &msg); err != nil {
			panic("hyperpb: protojson decode: " + err.Error())
		}
		wire, err := proto.Marshal(&msg)
		if err != nil {
			panic("hyperpb: marshal: " + err.Error())
		}
		hyperpbWireData = append([]byte(nil), wire...)
	})
	return hyperpbWireData
}

func loadKubePodsHyperPBType() *hyperpb.MessageType {
	hyperpbMsgTypeOnce.Do(func() {
		md := kubepods.File_kubepods_proto.Messages().ByName("KubePodList")
		hyperpbMsgType = hyperpb.CompileMessageDescriptor(md)
	})
	return hyperpbMsgType
}

// =============================================================================
// Marshal: KubePods
// =============================================================================

func Benchmark_Marshal_KubePods_Fory(b *testing.B) {
	v := loadKubePodsForyXlangValue()
	f := newForyXlang()
	probe, err := f.Serialize(v)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(probe)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := f.Serialize(v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Marshal_KubePods_ForyNative(b *testing.B) {
	v := loadKubePodsForyNativeValue()
	f := newForyNative()
	probe, err := f.Serialize(v)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(probe)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := f.Serialize(v); err != nil {
			b.Fatal(err)
		}
	}
}

// =============================================================================
// Unmarshal: KubePods
// =============================================================================

func Benchmark_Unmarshal_KubePods_Fory(b *testing.B) {
	wire := loadKubePodsForyXlangWire()
	f := newForyXlang()
	b.SetBytes(int64(len(wire)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var v KubePodList
		if err := f.Deserialize(wire, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePods_ForyNative(b *testing.B) {
	wire := loadKubePodsForyNativeWire()
	f := newForyNative()
	b.SetBytes(int64(len(wire)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var v KubePodList
		if err := f.Deserialize(wire, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePods_HyperPB(b *testing.B) {
	wire := loadKubePodsHyperPBWire()
	mt := loadKubePodsHyperPBType()
	b.SetBytes(int64(len(wire)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		msg := hyperpb.NewMessage(mt)
		if err := msg.Unmarshal(wire); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePods_ProtobufGo(b *testing.B) {
	wire := loadKubePodsHyperPBWire()
	b.SetBytes(int64(len(wire)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var msg kubepods.KubePodList
		if err := proto.Unmarshal(wire, &msg); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePods_VtProto(b *testing.B) {
	wire, _ := vtproto.LoadKubePodsWire(KubePodsJSON)
	b.SetBytes(int64(len(wire)))
	b.ReportAllocs()
	for b.Loop() {
		if err := vtproto.UnmarshalKubePodsVT(wire); err != nil {
			b.Fatal(err)
		}
	}
}

func Benchmark_Unmarshal_KubePods_VtProtoUnsafe(b *testing.B) {
	wire, _ := vtproto.LoadKubePodsWire(KubePodsJSON)
	b.SetBytes(int64(len(wire)))
	b.ReportAllocs()
	for b.Loop() {
		if err := vtproto.UnmarshalKubePodsVTUnsafe(wire); err != nil {
			b.Fatal(err)
		}
	}
}
