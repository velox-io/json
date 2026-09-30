package bind

import "testing"

// needNativeBinder skips a test that pins native machine internals (tape
// layout, window yields, allocator state) the Go engine does not reproduce.
func needNativeBinder(t *testing.T) {
	t.Helper()
	if useGoCore() {
		t.Skip("pins native binder internals")
	}
}
