package realtime

import (
	"testing"

	"syntrix/internal/storage"

	"github.com/stretchr/testify/assert"
)

func TestCEL_TypeMismatch(t *testing.T) {
	// Filter: age > 20
	filters := []storage.Filter{
		{Field: "age", Op: ">", Value: 20},
	}
	prg, err := compileFiltersToCEL(filters)
	assert.NoError(t, err)

	// Case 1: age is int (25)
	out, _, err := prg.Eval(map[string]interface{}{
		"doc": map[string]interface{}{"age": 25},
	})
	assert.NoError(t, err)
	val, _ := out.Value().(bool)
	assert.True(t, val)

	// Case 2: age is float64 (25.0)
	out, _, err = prg.Eval(map[string]interface{}{
		"doc": map[string]interface{}{"age": 25.0},
	})
	assert.NoError(t, err, "CEL evaluation failed for float64 input against int literal")
	val, _ = out.Value().(bool)
	assert.True(t, val)
}
