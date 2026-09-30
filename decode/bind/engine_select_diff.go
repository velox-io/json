//go:build vj_enginediff

package bind

import (
	"sync"

	"github.com/velox-io/json/internal/gbind"
	"github.com/velox-io/json/native/ndec"
	"github.com/velox-io/json/vbind"
)

// The differential build routes binds per call, so one process can run both
// engines over the same input and compare. Engine choice is dynamic, so a
// shape's Go view is built lazily on the first Go bind after the shape is
// already shared; the sync.Once publishes the write.
var forceGoCore bool

func useGoCore() bool { return !ndec.Available || forceGoCore }

type goPlan struct {
	once sync.Once
	plan *gbind.Plan
}

func (g *goPlan) build(tt *vbind.TypeTree) {}

func (sh *shape) goPlan() *gbind.Plan {
	sh.gplan.once.Do(func() { sh.gplan.plan = gbind.NewPlan(sh.tt) })
	return sh.gplan.plan
}
