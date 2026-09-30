//go:build !vj_enginediff

package bind

import (
	"github.com/velox-io/json/internal/gbind"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// Engine selection is a build property: the Go engine serves every bind in
// vj_nondec builds and none otherwise, so useGoCore folds to a constant and
// the Go adapters drop out of native builds.
func useGoCore() bool { return !ndec.Available }

// goPlan is a shape's engine view. build constructs it in vj_nondec builds,
// where every bind needs it; native builds store none.
type goPlan struct{ plan *gbind.Plan }

// build runs inside shapeFor's cache closure, whose publication orders the
// write before any goroutine can share the shape.
func (g *goPlan) build(tt *vbind.TypeTree) {
	if ndec.Available {
		return
	}
	g.plan = gbind.NewPlan(tt)
}

func (sh *shape) goPlan() *gbind.Plan { return sh.gplan.plan }
