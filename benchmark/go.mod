module dev.local/benchmark

go 1.27

require (
	buf.build/go/hyperpb v0.1.3
	github.com/apache/fory/go/fory v1.5.0
	github.com/bytedance/sonic v1.15.3-0.20260730064818-2a36d6da63e2
	github.com/goccy/go-json v0.10.5
	github.com/klauspost/compress v1.18.4
	github.com/planetscale/vtprotobuf v0.6.0
	github.com/velox-io/json v0.0.0
	golang.design/x/reflect v0.1.1
	google.golang.org/protobuf v1.36.11
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic/loader v0.5.2 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/klauspost/cpuid/v2 v2.2.9 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	github.com/timandy/routine v1.1.5 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/valyala/fastjson v1.6.10 // indirect
	golang.org/x/arch v0.0.0-20210923205945-b76863e36670 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

replace github.com/velox-io/json => ..
