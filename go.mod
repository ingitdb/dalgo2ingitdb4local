module github.com/ingitdb/dalgo2ingitdb4local

go 1.26.0

require (
	github.com/dal-go/dalgo v0.46.1
	github.com/ingitdb/dalgo2ingitdb v0.0.0
	github.com/ingitdb/ingitdb-go/ingitdb v0.0.0
	github.com/pelletier/go-toml/v2 v2.3.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/RoaringBitmap/roaring/v2 v2.18.2 // indirect
	github.com/bits-and-blooms/bitset v1.24.4 // indirect
	github.com/gofrs/flock v0.13.0 // indirect
	github.com/ingr-io/ingr-go v0.0.2 // indirect
	github.com/mschoch/smat v0.2.0 // indirect
	github.com/strongo/random v0.0.1 // indirect
	go.starlark.net v0.0.0-20260613233743-8ba36ccb83fb // indirect
	golang.org/x/sys v0.42.0 // indirect
)

replace (
	github.com/ingitdb/dalgo2ingitdb => ../dalgo2ingitdb
	github.com/ingitdb/ingitdb-go/ingitdb => ../ingitdb-go/ingitdb
)
