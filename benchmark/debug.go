package benchmark

var ballast []byte

func init() {
	ballast = make([]byte, 64<<20)
	ballast[0] = 1
}
