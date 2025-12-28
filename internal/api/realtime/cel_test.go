package realtime

import (
	"testing"

	"github.com/codetrek/syntrix/pkg/model"
	"github.com/stretchr/testify/assert"
)

func TestCEL_TypeMismatch(t *testing.T) {
	// Filter: age > 20
	filters := []model.Filter{
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
	cases := []model.Filter{
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
	_, err := filterToExpression(model.Filter{Field: "age", Op: "!=", Value: 1})
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

func TestCompileFiltersToCEL_Empty(t *testing.T) {
	prg, err := compileFiltersToCEL(nil)
	assert.NoError(t, err)
	assert.Nil(t, prg)

	prg, err = compileFiltersToCEL([]model.Filter{})
	assert.NoError(t, err)
	assert.Nil(t, prg)
}

func TestCompileFiltersToCEL_FilterError(t *testing.T) {
	// Unsupported operator
	filters := []model.Filter{
		{Field: "age", Op: "invalid_op", Value: 10},
	}
	_, err := compileFiltersToCEL(filters)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported operator")
}

func TestCompileFiltersToCEL_ValueError(t *testing.T) {
	// Unsupported value type
	filters := []model.Filter{
		{Field: "age", Op: "==", Value: map[string]interface{}{"a": 1}},
	}
	_, err := compileFiltersToCEL(filters)
	assert.Error(t, err)
}

func TestFormatValue_NestedError(t *testing.T) {
	// Slice with unsupported type
	val := []interface{}{
		"valid",
		map[string]interface{}{"invalid": true},
	}
	_, err := formatValue(val)
	assert.Error(t, err)
}

func TestCompileFiltersToCEL_CompileError(t *testing.T) {
	// Trigger compile error by using a field name containing a single quote,
	// which is not escaped by the current implementation of filterToExpression.
	filters := []model.Filter{
		{Field: "foo'bar", Op: "==", Value: 1},
	}
	_, err := compileFiltersToCEL(filters)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CEL compile error")
}
