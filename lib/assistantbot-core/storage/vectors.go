package storage

import (
	"fmt"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/ncruces"
)

// embeddingArg serializes vectors for sqlite-vec. Binary BLOB is preferred over JSON text.
func embeddingArg(values []float32) (any, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("embedding is empty")
	}
	return sqlitevec.SerializeFloat32(values)
}
