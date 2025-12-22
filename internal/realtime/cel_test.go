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

func TestFilterToExpression_AllOperators(t *testing.T) {
	cases := []storage.Filter{
		{Field: "age", Op: "==", Value: 10},
		{Field: "age", Op: ">", Value: 1},
		{Field: "age", Op: ">=", Value: 1},
		{Field: "age", Op: "<", Value: 1},
		{Field: "age", Op: "<=", Value: 1},
		{Field: "role", Op: "in", Value: []interface{}{"admin"}},
		{Field: "tags", Op: "array-contains", Value: "go"},
	}

	for _, c := range cases {
		t.Run(c.Op, func(t *testing.T) {
			_, err := filterToExpression(c)
			assert.NoError(t, err)
		})
	}
}

func TestFilterToExpression_Unsupported(t *testing.T) {
	_, err := filterToExpression(storage.Filter{Field: "age", Op: "!=", Value: 1})
	assert.Error(t, err)
}

func TestFormatValue_VariousTypes(t *testing.T) {
	// Supported types
	_, err := formatValue(true)
	assert.NoError(t, err)

	_, err = formatValue([]interface{}{"a", 1, false})
	assert.NoError(t, err)

	// Unsupported type
	_, err = formatValue(map[string]interface{}{"x": 1})
	assert.Error(t, err)
}
