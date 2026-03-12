module dev.local/benchmark

go 1.24.0

toolchain go1.24.1

require (
	github.com/bytedance/sonic v1.15.0
	github.com/urfave/cli/v3 v3.6.2
	github.com/velox-io/json v0.0.0
)

require (
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/klauspost/cpuid/v2 v2.2.9 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	golang.org/x/arch v0.0.0-20210923205945-b76863e36670 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

replace github.com/velox-io/json => ..
