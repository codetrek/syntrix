package authz

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/codetrek/syntrix/internal/query"
	"github.com/codetrek/syntrix/pkg/model"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"gopkg.in/yaml.v3"
)

type Engine interface {
	Evaluate(ctx context.Context, path string, action string, req Request, existingRes *Resource) (bool, error)
	GetRules() *RuleSet
	UpdateRules(content []byte) error
	LoadRules(path string) error
}

type ruleEngine struct {
	rules      *RuleSet
	celEnv     *cel.Env
	programMap sync.Map // map[string]cel.Program
	query      query.Service
}

func NewEngine(q query.Service) (Engine, error) {
	// Define CEL environment
	env, err := cel.NewEnv(
		cel.Declarations(
			decls.NewVar("request", decls.NewMapType(decls.String, decls.Dyn)),
			decls.NewVar("resource", decls.NewMapType(decls.String, decls.Dyn)),
		),
		cel.Lib(newAuthzLib(q)),
	)
	if err != nil {
		return nil, err
	}

	return &ruleEngine{
		celEnv: env,
		query:  q,
	}, nil
}

func (e *ruleEngine) LoadRules(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var rules RuleSet
	if err := yaml.Unmarshal(data, &rules); err != nil {
		return err
	}

	e.rules = &rules
	e.programMap = sync.Map{}
	return nil
}

func (e *ruleEngine) Evaluate(ctx context.Context, path string, action string, req Request, existingRes *Resource) (bool, error) {
	if e.rules == nil {
		return false, nil
	}

	fullPath := "/databases/default/documents/" + strings.TrimPrefix(path, "/")
	return e.matchPath(ctx, e.rules.Match, fullPath, action, make(map[string]string), req, existingRes)
}

func (e *ruleEngine) matchPath(ctx context.Context, blocks map[string]MatchBlock, path string, action string, vars map[string]string, req Request, existingRes *Resource) (bool, error) {
	if len(blocks) == 0 {
		return false, nil
	}

	for pattern, block := range blocks {
		matched, remaining, newVars := matchPattern(pattern, path)
		if matched {
			childVars := make(map[string]string)
			for k, v := range vars {
				childVars[k] = v
			}
			for k, v := range newVars {
				childVars[k] = v
			}

			if remaining == "" || remaining == "/" {
				return e.evaluateAllow(ctx, block.Allow, action, childVars, req, existingRes)
			}

			return e.matchPath(ctx, block.Match, remaining, action, childVars, req, existingRes)
		}
	}

	return false, nil
}

func matchPattern(pattern, path string) (bool, string, map[string]string) {
	pattern = strings.TrimPrefix(pattern, "/")
	path = strings.TrimPrefix(path, "/")

	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")

	if len(pathParts) < len(patternParts) {
		return false, "", nil
	}

	vars := make(map[string]string)

	for i, pPart := range patternParts {
		if strings.HasPrefix(pPart, "{") && strings.HasSuffix(pPart, "}") {
			varName := pPart[1 : len(pPart)-1]
			if strings.HasSuffix(varName, "=**") {
				realVarName := strings.TrimSuffix(varName, "=**")
				vars[realVarName] = strings.Join(pathParts[i:], "/")
				return true, "", vars
			}
			vars[varName] = pathParts[i]
		} else {
			if pPart != pathParts[i] {
				return false, "", nil
			}
		}
	}

	remaining := strings.Join(pathParts[len(patternParts):], "/")
	if remaining != "" {
		remaining = "/" + remaining
	}
	return true, remaining, vars
}

func (e *ruleEngine) evaluateAllow(ctx context.Context, allow map[string]string, action string, vars map[string]string, req Request, existingRes *Resource) (bool, error) {
	for actionsStr, condition := range allow {
		actions := strings.Split(actionsStr, ",")
		for _, a := range actions {
			a = strings.TrimSpace(a)
			if matchesAction(a, action) {
				result, err := e.evalCondition(ctx, condition, vars, req, existingRes)
				if err != nil {
					return false, err
				}
				if result {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func matchesAction(ruleAction, reqAction string) bool {
	if ruleAction == reqAction {
		return true
	}
	if ruleAction == "read" && (reqAction == "get" || reqAction == "list") {
		return true
	}
	if ruleAction == "write" && (reqAction == "create" || reqAction == "update" || reqAction == "delete") {
		return true
	}
	return false
}

func (e *ruleEngine) evalCondition(ctx context.Context, condition string, vars map[string]string, req Request, existingRes *Resource) (bool, error) {
	for k, v := range vars {
		condition = strings.ReplaceAll(condition, "$("+k+")", v)
	}

	prg, err := e.getProgram(condition, vars)
	if err != nil {
		return false, err
	}

	input := map[string]interface{}{
		"request":  structToMap(req),
		"resource": structToMap(existingRes),
	}
	for k, v := range vars {
		input[k] = v
	}

	out, _, err := prg.Eval(input)
	if err != nil {
		return false, err
	}

	return out.Value() == true, nil
}

func (e *ruleEngine) getProgram(expression string, vars map[string]string) (cel.Program, error) {
	varOpts := []cel.EnvOption{}
	for k := range vars {
		varOpts = append(varOpts, cel.Declarations(decls.NewVar(k, decls.String)))
	}

	env, err := e.celEnv.Extend(varOpts...)
	if err != nil {
		return nil, err
	}

	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		return nil, issues.Err()
	}

	return env.Program(ast)
}

func structToMap(v interface{}) map[string]interface{} {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	var m map[string]interface{}
	json.Unmarshal(b, &m)
	return m
}

type authzLib struct {
	query query.Service
}

func newAuthzLib(q query.Service) *authzLib {
	return &authzLib{query: q}
}

func (l *authzLib) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("exists",
			cel.Overload("exists_string",
				[]*cel.Type{cel.StringType},
				cel.BoolType,
				cel.UnaryBinding(l.exists),
			),
		),
		cel.Function("get",
			cel.Overload("get_string",
				[]*cel.Type{cel.StringType},
				cel.MapType(cel.StringType, cel.DynType),
				cel.UnaryBinding(l.get),
			),
		),
	}
}

func (l *authzLib) ProgramOptions() []cel.ProgramOption {
	return []cel.ProgramOption{}
}

func (l *authzLib) exists(arg ref.Val) ref.Val {
	path, ok := arg.(types.String)
	if !ok {
		return types.NewErr("invalid argument to exists")
	}
	internalPath := stripDatabasePrefix(string(path))

	_, err := l.query.GetDocument(context.Background(), internalPath)
	if err == model.ErrNotFound {
		return types.Bool(false)
	}
	if err != nil {
		return types.NewErr("error in exists: %v", err)
	}
	return types.Bool(true)
}

func (l *authzLib) get(arg ref.Val) ref.Val {
	path, ok := arg.(types.String)
	if !ok {
		return types.NewErr("invalid argument to get")
	}
	internalPath := stripDatabasePrefix(string(path))

	doc, err := l.query.GetDocument(context.Background(), internalPath)
	if err != nil {
		return types.NewErr("error in get: %v", err)
	}

	data := model.Document{}
	for k, v := range doc {
		data[k] = v
	}
	data.StripProtectedFields()

	res := map[string]interface{}{
		"data": data,
		"id":   doc.GetID(),
	}
	return types.DefaultTypeAdapter.NativeToValue(res)
}

func stripDatabasePrefix(path string) string {
	parts := strings.Split(path, "/documents/")
	if len(parts) > 1 {
		return parts[1]
	}
	return path
}

func (e *ruleEngine) GetRules() *RuleSet {
	return e.rules
}

func (e *ruleEngine) UpdateRules(content []byte) error {
	var rules RuleSet
	if err := yaml.Unmarshal(content, &rules); err != nil {
		return err
	}

	// Validate rules? (e.g. check if CEL expressions compile)
	// For now, just update.
	e.rules = &rules
	e.programMap = sync.Map{} // Clear cache
	return nil
}
