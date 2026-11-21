package javaport

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

type javaLambdaAdapter struct {
	name       string
	interfaces map[string]*javaInterface
	method     *javaInterfaceMethod
	defaults   []*javaInterfaceMethod
	closeable  bool
}

type javaLambdaCaptures struct {
	id      int
	outer   *javaLambdaCaptures
	visible map[string]bool
	copies  map[string]string
}

func (capture *javaLambdaCaptures) has(name string) bool {
	return capture != nil && capture.visible[name]
}

func (capture *javaLambdaCaptures) resolve(name string) string {
	if !capture.has(name) {
		return name
	}
	if copy, present := capture.copies[name]; present {
		return copy
	}
	copy := fmt.Sprintf("javaCaptured%d_%d", capture.id, len(capture.copies))
	capture.copies[name] = copy
	return copy
}

func (ctx javaObjectContext) lambdaScope(id int) javaObjectContext {
	sources := map[string]bool{}
	if ctx.lambdaCaptures != nil {
		maps.Copy(sources, ctx.lambdaCaptures.visible)
	}
	for _, bindings := range []map[string]string{ctx.locals, ctx.arguments, ctx.caught} {
		for _, name := range bindings {
			sources[name] = true
		}
	}
	for pattern := range ctx.patterns {
		sources[javaPatternName(pattern)] = true
	}
	ctx = ctx.patternScope()
	ctx.arguments = maps.Clone(ctx.arguments)
	if ctx.arguments == nil {
		ctx.arguments = map[string]string{}
	}
	ctx.lambdaCaptures = &javaLambdaCaptures{
		id: id, outer: ctx.lambdaCaptures, visible: sources, copies: map[string]string{},
	}
	// Java return, break, continue and yield cannot target an enclosing
	// statement outside this lambda invocation.
	ctx.tryScope, ctx.control, ctx.yield = nil, nil, nil
	return ctx
}

func splitJavaIntersection(javaType string) []string {
	if !strings.Contains(javaType, " & ") {
		return nil
	}
	return strings.Split(javaType, " & ")
}

func (program *javaObjectProgram) supportedIntersection(javaType string) bool {
	bounds := splitJavaIntersection(javaType)
	return len(bounds) != 0 && !slices.ContainsFunc(bounds, func(bound string) bool {
		return program.interfaces[bound] == nil
	})
}

func (ctx javaObjectContext) castIntersection(operand *JavaExpression, target string) (string, error) {
	value, err := ctx.expression(operand)
	if err != nil {
		return "", err
	}
	return ctx.checkIntersection(value, target), nil
}

func (ctx javaObjectContext) checkIntersection(value, target string) string {
	var code strings.Builder
	fmt.Fprintf(&code, "func() any {\n\tjavaIntersection := %s\n", ctx.program.primitiveLowering.canonicalReference(value))
	for _, bound := range splitJavaIntersection(target) {
		check := ctx.interfaceReferenceCall("CastInterface", ctx.program.interfaces[bound], "javaIntersection")
		fmt.Fprintf(&code, "\t_ = %s\n", check)
	}
	code.WriteString("\treturn javaIntersection\n}()")
	return code.String()
}

func (program *javaObjectProgram) lambdaAdapter(target string) (*javaLambdaAdapter, error) {
	bounds := splitJavaIntersection(target)
	if len(bounds) == 0 {
		bounds = []string{target}
	}
	interfaces := map[string]*javaInterface{}
	closeable := false
	for _, bound := range bounds {
		iface := program.interfaces[bound]
		if iface == nil {
			return nil, fmt.Errorf("unsupported Java lambda target %s", bound)
		}
		maps.Copy(interfaces, iface.supers)
		closeable = closeable || iface.closeable
	}
	key := strings.Join(slices.Sorted(maps.Keys(interfaces)), " & ")
	if adapter := program.lambdaAdapters[key]; adapter != nil {
		return adapter, nil
	}
	contracts, err := interfaceContracts(interfaces)
	if err != nil {
		return nil, err
	}
	adapter := &javaLambdaAdapter{name: fmt.Sprintf("javaLambdaAdapter%d", len(program.lambdaAdapters)), interfaces: interfaces, closeable: closeable}
	for _, name := range slices.Sorted(maps.Keys(contracts)) {
		contract := contracts[name]
		if method := mostSpecificDefault(contract); method != nil {
			adapter.defaults = append(adapter.defaults, method)
			continue
		}
		index := slices.IndexFunc(contract, func(method *javaInterfaceMethod) bool { return !method.isDefault })
		if adapter.method != nil || index == -1 {
			return nil, fmt.Errorf("cannot map a single Java function method for %s", target)
		}
		adapter.method = contract[index]
	}
	if adapter.method == nil {
		return nil, fmt.Errorf("missing Java function method for %s", target)
	}
	if closeable && len(contracts["Close"]) == 0 {
		return nil, fmt.Errorf("unsupported implicit AutoCloseable function method for %s", target)
	}
	program.lambdaAdapters[key] = adapter
	return adapter, nil
}

func (ctx javaObjectContext) lambda(expression *JavaExpression) (string, error) {
	adapter, err := ctx.program.lambdaAdapter(expression.Type)
	if err != nil {
		return "", err
	}
	declaration := adapter.method.declaration
	if len(expression.Parameters) != len(declaration.Parameters) {
		return "", fmt.Errorf("unsupported attributed Java lambda arity for %s", expression.Type)
	}
	id := ctx.program.lambdaNext
	ctx.program.lambdaNext++
	child := ctx.lambdaScope(id)
	parameters := make([]string, 0, len(expression.Parameters))
	for index, parameter := range expression.Parameters {
		javaType := declaration.Parameters[index].Type
		if parameter.Type != javaType {
			return "", fmt.Errorf("unsupported Java lambda parameter conversion from %s to %s", javaType, parameter.Type)
		}
		name := fmt.Sprintf("javaLambdaArgument%d_%d", id, index)
		child.arguments[parameter.Name] = name
		parameters = append(parameters, name+" "+ctx.program.objectGoType(javaType))
	}
	result := ctx.program.interfaceResult(declaration)
	hasTry := expression.Body.containsTry() || expression.Operand.containsStatement(func(kind string) bool { return kind == "TRY" })
	if hasTry && result != "" {
		result = " (javaResult " + ctx.program.objectGoType(declaration.Result) + ")"
	}
	var body strings.Builder
	if hasTry {
		child.emitJavaReturnBoundary(&body, declaration.Result)
	}
	switch {
	case expression.Body != nil:
		if err := child.emitStatements(&body, expression.Body, declaration.Result); err != nil {
			return "", err
		}
		if result != "" && (hasTry || expression.Body.containsStatement(needsJavaControlFlowTerminal)) {
			body.WriteString("\tpanic(\"Java lambda completed without a result\")\n")
		}
	case expression.Operand != nil:
		var value string
		if result == "" {
			value, err = child.expression(expression.Operand)
		} else {
			value, err = child.converted(expression.Operand, declaration.Result)
		}
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&body, "\t%s%s\n", returnKeyword(result), value)
	default:
		return "", fmt.Errorf("missing attributed Java lambda body")
	}
	return child.makeLambda(adapter, strings.Join(parameters, ", "), result, &body), nil
}

func (ctx javaObjectContext) makeLambda(adapter *javaLambdaAdapter, parameters, result string, body *strings.Builder) string {
	var code strings.Builder
	fmt.Fprintf(&code, "func() *%s {\n", adapter.name)
	capture := ctx.lambdaCaptures
	for _, source := range slices.Sorted(maps.Keys(capture.copies)) {
		fmt.Fprintf(&code, "\t%s := %s\n", capture.copies[source], capture.outer.resolve(source))
	}
	fmt.Fprintf(&code, "\treturn &%s{invoke: func(%s)%s {\n", adapter.name, parameters, result)
	ctx.writePatternBody(&code, body)
	code.WriteString("\t}}\n}()")
	return code.String()
}

func (program *javaObjectProgram) emitLambdaAdapters(out *strings.Builder) {
	for _, key := range slices.Sorted(maps.Keys(program.lambdaAdapters)) {
		adapter := program.lambdaAdapters[key]
		method := adapter.method
		parameters, arguments := program.objectParameterList(method.declaration.Parameters)
		result := program.interfaceResult(method.declaration)
		fmt.Fprintf(out, "\ntype %s struct { invoke func(%s)%s }\n", adapter.name, parameters, result)
		fmt.Fprintf(out, "\nfunc (lambda *%s) %s(%s)%s { %slambda.invoke(%s) }\n",
			adapter.name, method.goName, parameters, result, returnKeyword(result), arguments)
		for _, defaultMethod := range adapter.defaults {
			parameters, arguments := program.objectParameterList(defaultMethod.declaration.Parameters)
			result := program.interfaceResult(defaultMethod.declaration)
			ctx := javaObjectContext{program: program}
			fmt.Fprintf(out, "\nfunc (lambda *%s) %s(%s)%s { %s%s }\n", adapter.name, defaultMethod.goName, parameters, result,
				returnKeyword(result), ctx.defaultCall(defaultMethod, "lambda", arguments))
		}
		for _, marker := range slices.Sorted(maps.Keys(interfaceMarkers(adapter.interfaces))) {
			fmt.Fprintf(out, "\nfunc (*%s) %s() {}\n", adapter.name, marker)
		}
		if adapter.closeable {
			fmt.Fprintf(out, "\nfunc (*%s) GppAutoCloseable() {}\n", adapter.name)
		}
		interfaces := slices.Sorted(maps.Keys(adapter.interfaces))
		for index, name := range interfaces {
			interfaces[index] = strconv.Quote(name)
		}
		fmt.Fprintf(out, "\nfunc (*%s) %s(javaInterface string) bool {\n\tswitch javaInterface {\n\tcase %s:\n\t\treturn true\n\t}\n\treturn false\n}\n",
			adapter.name, javaImplementsBridge, strings.Join(interfaces, ", "))
	}
}
