package realtime

import (
	"fmt"
	"strings"

	"github.com/codetrek/syntrix/pkg/model"
	"github.com/google/cel-go/cel"
)

func compileFiltersToCEL(filters []model.Filter) (cel.Program, error) {
	if len(filters) == 0 {
		return nil, nil
	}

	var expressions []string
	for _, f := range filters {
		expr, err := filterToExpression(f)
		if err != nil {
			return nil, err
		}
		expressions = append(expressions, expr)
	}

	fullExpr := strings.Join(expressions, " && ")

	env, err := cel.NewEnv(
		cel.Variable("doc", cel.MapType(cel.StringType, cel.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create CEL env: %w", err)
	}

	ast, issues := env.Compile(fullExpr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("CEL compile error: %w", issues.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("CEL program creation error: %w", err)
	}

	return prg, nil
}

func filterToExpression(f model.Filter) (string, error) {
	valStr, err := formatValue(f.Value)
	if err != nil {
		return "", err
	}

	field := "doc"
	parts := strings.Split(f.Field, ".")
	for _, p := range parts {
		// Use index syntax for safety against special characters in field names
		field += fmt.Sprintf("['%s']", p)
	}

	switch f.Op {
	case "==":
		return fmt.Sprintf("%s == %s", field, valStr), nil
	case ">":
		return fmt.Sprintf("%s > %s", field, valStr), nil
	case ">=":
		return fmt.Sprintf("%s >= %s", field, valStr), nil
	case "<":
		return fmt.Sprintf("%s < %s", field, valStr), nil
	case "<=":
		return fmt.Sprintf("%s <= %s", field, valStr), nil
	case "in":
		// field in [values]
		return fmt.Sprintf("%s in %s", field, valStr), nil
	case "array-contains":
		// value in field
		return fmt.Sprintf("%s in %s", valStr, field), nil
	default:
		return "", fmt.Errorf("unsupported operator: %s", f.Op)
	}
}

func formatValue(v interface{}) (string, error) {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("'%s'", strings.ReplaceAll(val, "'", "\\'")), nil
	case int, int32, int64:
		return fmt.Sprintf("%d", val), nil
	case float32, float64:
		return fmt.Sprintf("%v", val), nil
	case bool:
		return fmt.Sprintf("%v", val), nil
	case []interface{}:
		var parts []string
		for _, item := range val {
			s, err := formatValue(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, s)
		}
		return fmt.Sprintf("[%s]", strings.Join(parts, ", ")), nil
	default:
		return "", fmt.Errorf("unsupported value type: %T", v)
	}
}
