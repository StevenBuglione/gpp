package compiler

import (
	"fmt"
	"go/types"
	"sort"
	"strconv"
	"strings"
)

const classScopeCallPrefix = "__gppClassCall_"
const staticScopeCallPrefix = "__gppStaticCall_"

func packageCallNamed(name string, context constructorContext) bool {
	return len(context.FunctionSignatures[name]) != 0 || context.GlobalValueTypes[name] != "" ||
		context.Targets[name].Class != nil || context.StructConstructors[name] != nil || types.Universe.Lookup(name) != nil
}

func methodDeclarationKey(method Method) string {
	return methodSignatureKey(method) + "/" + strconv.Itoa(len(method.TypeParamsAST))
}

func callableDeclarationKey(signature callableSignature) string {
	parts := make([]string, 0, len(signature.Parameters))
	for _, parameter := range signature.Parameters {
		parts = append(parts, strings.Join(strings.Fields(typeNodeSignatureKey(parameter.TypeAST)), " "))
	}
	return signature.Name + "/" + strings.Join(parts, ",") + "/" + strconv.Itoa(len(signature.TypeParams))
}

type classScopeCandidate struct {
	signature callableSignature
	instance  bool
}

type classScopeResolution struct {
	classScopeCandidate
	selector     *SelectorExpr
	typeArgs     []ExprNode
	arguments    []CallArg
	typeBindings map[string]string
	receiver     string
	score        int
	variadic     bool
}

// Resolve lexical class calls and qualified static overloads through the same
// typed selector. A lexical call can select either kind of member.
func resolveClassScopeCall(call *CallExpr, context constructorContext, valueTypes map[string]string) (classScopeResolution, bool, error) {
	selector, _, ok := overloadGenericMethodSelector(call.Callee)
	if !ok {
		return classScopeResolution{}, false, nil
	}
	receiver, ok := directSelectorPath(selector.Receiver)
	if !ok {
		return classScopeResolution{}, false, nil
	}
	className, staticContext := strings.CutPrefix(receiver, staticScopeCallPrefix)
	bound := staticContext
	if !bound {
		className, bound = strings.CutPrefix(receiver, classScopeCallPrefix)
	}
	if !bound {
		className = receiver
	}
	statics := context.StaticMethodSignatures[className][selector.Name]
	if !bound && (len(statics) < 2 || valueTypes[strings.SplitN(receiver, ".", 2)[0]] != "") {
		return classScopeResolution{}, false, nil
	}

	var explicit []ExprNode
	switch callee := call.Callee.(type) {
	case *IndexExpr:
		explicit = []ExprNode{callee.Index}
	case *IndexListExpr:
		explicit = callee.Indices
	}

	var candidates []classScopeCandidate
	instanceKeys := map[string]bool{}
	if bound {
		for _, instance := range context.ClassMethodSignatures[className][selector.Name] {
			key := callableDeclarationKey(instance)
			if !instanceKeys[key] {
				candidates = append(candidates, classScopeCandidate{signature: instance, instance: true})
				instanceKeys[key] = true
			}
		}
	}
	seenStatic := map[string]bool{}
	for _, signature := range statics {
		key := signature.Qualifier + "/" + signature.GoName
		if seenStatic[key] || (!staticContext && instanceKeys[callableDeclarationKey(signature)]) {
			continue
		}
		seenStatic[key] = true
		candidates = append(candidates, classScopeCandidate{signature: signature})
	}

	values := make([]string, len(explicit))
	for index, argument := range explicit {
		values[index], _ = expressionNodeSource(argument)
	}
	var matches []classScopeResolution
	for _, candidate := range candidates {
		signature := qualifyClassScopeSignature(candidate.signature, className, context)
		candidate.signature = signature
		bindings := map[string]string{}
		params := signature.TypeParams
		if signature.StaticSelf {
			bindings["self"] = className
			if len(params) > 0 {
				params = params[:len(params)-1]
			}
		}
		if len(explicit) != 0 && len(explicit) != len(params) {
			continue
		}
		for index, value := range values {
			bindings[params[index].Name] = value
		}
		arguments, ok := classScopeArguments(call.Arguments, signature)
		if !ok {
			continue
		}
		score := classScopeArgumentScore(arguments, signature, bindings, valueTypes, context)
		if score < 0 && len(candidates) > 1 {
			continue
		}
		matches = append(matches, classScopeResolution{
			classScopeCandidate: candidate, selector: selector, typeArgs: explicit, arguments: arguments,
			typeBindings: bindings, receiver: className, score: score, variadic: variadicParameterIndex(signature) >= 0,
		})
	}
	label := "method"
	if !bound {
		label = "static method"
	}
	if len(matches) == 0 {
		return classScopeResolution{}, true, sourceLineError(call.Span(), fmt.Errorf("no matching %s %s.%s", label, className, selector.Name))
	}
	best, unique := bestClassScopeMatch(matches, context)
	if !unique {
		return classScopeResolution{}, true, sourceLineError(call.Span(), fmt.Errorf("ambiguous %s %s.%s", label, className, selector.Name))
	}
	if staticContext && best.instance {
		return classScopeResolution{}, true, sourceLineError(call.Span(), fmt.Errorf("instance method %s.%s cannot be called from a static context", className, selector.Name))
	}
	return best, true, nil
}

// Parameter and result types are stored in the declaration's package.
// Qualify them in the caller's namespace before comparing argument types.
func qualifyClassScopeSignature(signature callableSignature, receiver string, context constructorContext) callableSignature {
	class := signature.DeclaringClass
	if class == nil || class.Owner == nil || class.Owner.Package == context.Package {
		return signature
	}
	alias := signature.Qualifier
	if alias == "" {
		alias = context.Targets[receiver].Qualifier
	}
	target, found := context.Targets[alias+"."+class.Name]
	if !found || target.Class != class {
		var aliases []string
		for key, candidate := range context.Targets {
			if candidate.Class == class && candidate.Qualifier != "" && key == candidate.Qualifier+"."+class.Name {
				aliases = append(aliases, candidate.Qualifier)
			}
		}
		if len(aliases) == 0 {
			return signature
		}
		sort.Strings(aliases)
		alias = aliases[0]
		target = context.Targets[alias+"."+class.Name]
	}
	names := make(map[string]bool, len(target.Classes)+len(context.ImportedPackageTypes[alias]))
	for name := range target.Classes {
		names[name] = true
	}
	for name := range context.ImportedPackageTypes[alias] {
		names[name] = true
	}
	for _, parameter := range signature.TypeParams {
		delete(names, parameter.Name)
	}
	visible := map[string]map[string]bool{alias: names}
	qualify := func(node TypeNode) TypeNode {
		return qualifyImportedTypeNode(node, alias+"."+class.Name, nil, visible)
	}
	signature.Parameters = append([]parameterInfo(nil), signature.Parameters...)
	for index := range signature.Parameters {
		signature.Parameters[index].TypeAST = qualify(signature.Parameters[index].TypeAST)
	}
	signature.ResultAST = qualify(signature.ResultAST)
	return signature
}

func bestClassScopeMatch(matches []classScopeResolution, context constructorContext) (classScopeResolution, bool) {
	// Prefer applicability without packing a variadic tail.
	fixed := false
	for _, match := range matches {
		fixed = fixed || !match.variadic
	}
	// First gather every equally ranked candidate. A more-specific
	// reference overload must win against all of them.
	var best []classScopeResolution
	for _, match := range matches {
		if fixed && match.variadic {
			continue
		}
		if len(best) == 0 || match.score > best[0].score {
			best = []classScopeResolution{match}
		} else if match.score == best[0].score {
			best = append(best, match)
		}
	}
	if len(best) == 1 {
		return best[0], true
	}
	for index, match := range best {
		specific := true
		for otherIndex, other := range best {
			if index != otherIndex && !moreSpecificClassScopeMatch(match, other, context) {
				specific = false
				break
			}
		}
		if specific {
			return match, true
		}
	}
	return classScopeResolution{}, false
}

func moreSpecificClassScopeMatch(left, right classScopeResolution, context constructorContext) bool {
	if len(left.arguments) != len(right.arguments) {
		return false
	}
	strict := false
	for index := range left.arguments {
		lhs := classScopeParameterType(left.signature, index, left.typeBindings)
		rhs := classScopeParameterType(right.signature, index, right.typeBindings)
		if lhs == rhs {
			continue
		}
		if rhs != "any" && !isAssignableStaticType(lhs, rhs, context) {
			return false
		}
		strict = true
	}
	return strict
}

func classScopeArguments(args []CallArg, signature callableSignature) ([]CallArg, bool) {
	params := signature.Parameters
	variadic := variadicParameterIndex(signature)
	if len(args) < requiredParameterCount(signature) || (len(args) > len(params) && variadic < 0) {
		return nil, false
	}
	named := len(args) != 0 && args[0].Name != ""
	if !named {
		for _, arg := range args {
			if arg.Name != "" {
				return nil, false
			}
			if _, spread := arg.Value.(*SpreadExpr); spread && variadic < 0 {
				return nil, false
			}
		}
		result := append([]CallArg(nil), args...)
		end := len(params)
		if variadic >= 0 {
			end = variadic
		}
		for index := len(args); index < end; index++ {
			if params[index].DefaultAST == nil {
				return nil, false
			}
			result = append(result, CallArg{Value: params[index].DefaultAST})
		}
		return result, true
	}

	result := make([]CallArg, len(params))
	for _, arg := range args {
		bound := false
		for index, param := range params {
			if param.Name == arg.Name && result[index].Value == nil {
				result[index], bound = CallArg{Value: arg.Value}, true
				break
			}
		}
		if !bound {
			return nil, false
		}
	}
	for index, param := range params {
		if result[index].Value != nil {
			continue
		}
		if index == variadic {
			return result[:index], true
		}
		if param.DefaultAST == nil {
			return nil, false
		}
		result[index] = CallArg{Value: param.DefaultAST}
	}
	return result, true
}

func classScopeParameterType(signature callableSignature, index int, bindings map[string]string) string {
	variadic := variadicParameterIndex(signature)
	if variadic >= 0 && index >= variadic {
		index = variadic
	}
	return strings.TrimPrefix(substituteLambdaType(signature.Parameters[index].typeText(), bindings), "...")
}

func classScopeArgumentScore(args []CallArg, signature callableSignature, bindings, valueTypes map[string]string, context constructorContext) int {
	score := 0
	generics := map[string]bool{}
	for _, parameter := range signature.TypeParams {
		if bindings[parameter.Name] == "" {
			generics[parameter.Name] = true
		}
	}
	for index, arg := range args {
		expected := classScopeParameterType(signature, index, bindings)
		expression := arg.Value
		if spread, ok := expression.(*SpreadExpr); ok {
			expression, expected = spread.Expression, "[]"+expected
		}
		actual := staticExpressionTypeNode(expression, context, valueTypes)
		switch {
		case actual == expected && actual != "":
			score += 4
		case actual == "nil":
			_, polymorphic := dispatchTargetForTypeName(expected, context)
			if !polymorphic && !isNilableGoType(expected) {
				return -1
			}
			score += 2
		case actual == "" || (isUnknownStaticArgumentNode(expression, context) && valueTypes[actual] == ""):
			// Remaining type checks are performed by Go.
		case isAssignableStaticType(actual, expected, context):
			score += 2
		case expected == "any" || containsGenericTypeParameter(expected, generics):
			score++
		default:
			return -1
		}
	}
	return score
}

func lowerClassScopeCall(call *CallExpr, context constructorContext) (*CallExpr, bool, error) {
	resolution, handled, err := resolveClassScopeCall(call, context, context.CurrentParameterTypes)
	if !handled || err != nil {
		return nil, handled, err
	}
	signature := resolution.signature
	var callee ExprNode
	if resolution.instance {
		callee = &SelectorExpr{Receiver: &NameExpr{Name: "this"}, Name: signature.GoName, SpanValue: resolution.selector.Span()}
	} else {
		callee = staticCallCalleeForSignature(resolution.selector, signature, context)
	}
	typeArgs := append([]ExprNode(nil), resolution.typeArgs...)
	if signature.StaticSelf {
		typeArgs = append(typeArgs, &TypeExpr{Type: parseTypeText(resolution.receiver)})
	}
	switch len(typeArgs) {
	case 0:
	case 1:
		callee = &IndexExpr{Receiver: callee, Index: typeArgs[0], SpanValue: call.Callee.Span()}
	default:
		callee = &IndexListExpr{Receiver: callee, Indices: typeArgs, SpanValue: call.Callee.Span()}
	}
	arguments := append([]CallArg(nil), resolution.arguments...)
	for index, argument := range arguments {
		lambda, ok := argument.Value.(*LambdaExpr)
		if !ok {
			continue
		}
		expected, _ := parseTypeText(classScopeParameterType(signature, index, resolution.typeBindings)).(*FunctionType)
		if expected == nil {
			continue
		}
		arguments[index].Value, err = lowerLambdaExprNode(lambda, context, []*FunctionType{expected})
		if err != nil {
			return nil, true, err
		}
	}
	return &CallExpr{Callee: callee, Arguments: arguments, SpanValue: call.Span()}, true, nil
}

func renderClassScopeCall(call *CallExpr, context constructorContext) (string, error) {
	callee, err := expressionNodeSource(call.Callee)
	if err != nil {
		return "", err
	}
	args := make([]string, len(call.Arguments))
	for index, arg := range call.Arguments {
		args[index], err = expressionNodeSource(arg.Value)
		if err != nil {
			return "", err
		}
	}
	args, err = transformCallableArgumentSources(args, context)
	if err != nil {
		return "", err
	}
	return callee + "(" + strings.Join(args, ", ") + ")", nil
}
