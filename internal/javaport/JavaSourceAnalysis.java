import com.sun.source.tree.ArrayAccessTree;
import com.sun.source.tree.AssignmentTree;
import com.sun.source.tree.BinaryTree;
import com.sun.source.tree.BlockTree;
import com.sun.source.tree.CatchTree;
import com.sun.source.tree.ClassTree;
import com.sun.source.tree.CompilationUnitTree;
import com.sun.source.tree.ConditionalExpressionTree;
import com.sun.source.tree.ExportsTree;
import com.sun.source.tree.ExpressionStatementTree;
import com.sun.source.tree.ExpressionTree;
import com.sun.source.tree.IdentifierTree;
import com.sun.source.tree.InstanceOfTree;
import com.sun.source.tree.IfTree;
import com.sun.source.tree.ImportTree;
import com.sun.source.tree.LiteralTree;
import com.sun.source.tree.MemberReferenceTree;
import com.sun.source.tree.MemberSelectTree;
import com.sun.source.tree.MethodInvocationTree;
import com.sun.source.tree.MethodTree;
import com.sun.source.tree.ModuleTree;
import com.sun.source.tree.NewArrayTree;
import com.sun.source.tree.NewClassTree;
import com.sun.source.tree.OpensTree;
import com.sun.source.tree.ParenthesizedTree;
import com.sun.source.tree.ProvidesTree;
import com.sun.source.tree.RequiresTree;
import com.sun.source.tree.ReturnTree;
import com.sun.source.tree.StatementTree;
import com.sun.source.tree.ThrowTree;
import com.sun.source.tree.Tree;
import com.sun.source.tree.TryTree;
import com.sun.source.tree.TypeCastTree;
import com.sun.source.tree.UnaryTree;
import com.sun.source.tree.UnionTypeTree;
import com.sun.source.tree.UsesTree;
import com.sun.source.tree.VariableTree;
import com.sun.source.util.JavacTask;
import com.sun.source.util.TreePath;
import com.sun.source.util.TreePathScanner;
import com.sun.source.util.Trees;
import java.nio.charset.Charset;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Comparator;
import java.util.HexFormat;
import java.util.List;
import java.util.Locale;
import java.util.TreeMap;
import java.util.stream.Collectors;
import javax.lang.model.element.AnnotationMirror;
import javax.lang.model.element.AnnotationValue;
import javax.lang.model.element.Element;
import javax.lang.model.element.ElementKind;
import javax.lang.model.element.ExecutableElement;
import javax.lang.model.element.Modifier;
import javax.lang.model.element.Parameterizable;
import javax.lang.model.element.TypeElement;
import javax.lang.model.element.TypeParameterElement;
import javax.lang.model.element.VariableElement;
import javax.lang.model.type.ArrayType;
import javax.lang.model.type.DeclaredType;
import javax.lang.model.type.ExecutableType;
import javax.lang.model.type.IntersectionType;
import javax.lang.model.type.TypeKind;
import javax.lang.model.type.TypeMirror;
import javax.lang.model.type.TypeVariable;
import javax.lang.model.type.WildcardType;
import javax.lang.model.util.Elements;
import javax.lang.model.util.Types;
import javax.tools.Diagnostic;
import javax.tools.DiagnosticCollector;
import javax.tools.JavaFileObject;
import javax.tools.ToolProvider;

class JavaSourceAnalysis {
    static final class Inventory extends TreePathScanner<Void, Void> {
        final CompilationUnitTree unit;
        final Trees trees;
        final Elements elements;
        final Types types;
        final boolean includeMethodBodies;
        final List<String> declarations = new ArrayList<>();
        final List<String> calls = new ArrayList<>();
        final TreeMap<String, String> typeReferences = new TreeMap<>();

        Inventory(CompilationUnitTree unit, Trees trees, Elements elements, Types types, boolean includeMethodBodies) {
            this.unit = unit;
            this.trees = trees;
            this.elements = elements;
            this.types = types;
            this.includeMethodBodies = includeMethodBodies;
        }

        @Override
        public Void visitClass(ClassTree tree, Void unused) {
            record(tree, declarations);
            return super.visitClass(tree, unused);
        }

        @Override
        public Void visitMethod(MethodTree tree, Void unused) {
            record(tree, declarations);
            return super.visitMethod(tree, unused);
        }

        @Override
        public Void visitBlock(BlockTree tree, Void unused) {
            if (includeMethodBodies && getCurrentPath().getParentPath().getLeaf() instanceof ClassTree) {
                record(tree, declarations);
            }
            return super.visitBlock(tree, unused);
        }

        @Override
        public Void visitVariable(VariableTree tree, Void unused) {
            var element = trees.getElement(getCurrentPath());
            if (element != null && (element.getKind().isField() ||
                    element.getKind() == ElementKind.RECORD_COMPONENT)) {
                record(tree, declarations);
            }
            return super.visitVariable(tree, unused);
        }

        @Override
        public Void visitMethodInvocation(MethodInvocationTree tree, Void unused) {
            record(tree, calls);
            return super.visitMethodInvocation(tree, unused);
        }

        @Override
        public Void visitNewClass(NewClassTree tree, Void unused) {
            record(tree, calls);
            return super.visitNewClass(tree, unused);
        }

        @Override
        public Void visitMemberReference(MemberReferenceTree tree, Void unused) {
            record(tree, calls);
            return super.visitMemberReference(tree, unused);
        }

        @Override
        public Void visitImport(ImportTree tree, Void unused) {
            // Unused Java imports do not introduce generated Go dependencies.
            return null;
        }

        @Override
        public Void visitIdentifier(IdentifierTree tree, Void unused) {
            recordTypeReference();
            return super.visitIdentifier(tree, unused);
        }

        @Override
        public Void visitMemberSelect(MemberSelectTree tree, Void unused) {
            recordTypeReference();
            return super.visitMemberSelect(tree, unused);
        }

        private void recordTypeReference() {
            var element = trees.getElement(getCurrentPath());
            while (element != null && !(element instanceof TypeElement)) {
                element = element.getEnclosingElement();
            }
            if (!(element instanceof TypeElement type)) {
                return;
            }
            String name = elements.getBinaryName(type).toString();
            if (name.isEmpty()) {
                return;
            }
            String module = moduleName(type);
            var pkg = elements.getPackageOf(type);
            typeReferences.putIfAbsent(module + "#" + name,
                "{\"binaryName\":" + quote(name) + ",\"moduleName\":" + quote(module) +
                ",\"package\":" + quote(pkg == null ? "" : pkg.getQualifiedName().toString()) + "}");
        }

        private String sourceModuleName() {
            for (var declaration : unit.getTypeDecls()) {
                if (trees.getElement(TreePath.getPath(unit, declaration)) instanceof TypeElement type) {
                    return moduleName(type);
                }
            }
            return unit.getModule() == null ? "" : unit.getModule().getName().toString();
        }

        private String moduleName(Element element) {
            var module = elements.getModuleOf(element);
            return module == null ? "" : module.getQualifiedName().toString();
        }

        private void record(Tree tree, List<String> output) {
            var positions = trees.getSourcePositions();
            long start = positions.getStartPosition(unit, tree);
            long end = positions.getEndPosition(unit, tree);
            var path = getCurrentPath();
            var element = trees.getElement(path);
            boolean synthetic = end == Diagnostic.NOPOS;
            if (synthetic && includeMethodBodies && tree instanceof MethodTree && element != null &&
                    element.getKind() == ElementKind.CONSTRUCTOR) {
                var enclosing = path.getParentPath().getLeaf();
                start = positions.getStartPosition(unit, enclosing);
                end = positions.getEndPosition(unit, enclosing);
            }
            if (start == Diagnostic.NOPOS || end == Diagnostic.NOPOS) {
                return;
            }
            var type = trees.getTypeMirror(path);
            output.add("{" + "\"kind\":" + quote(tree.getKind().name()) +
                ",\"symbol\":" + quote(symbol(element)) +
                ",\"type\":" + quote(type == null ? "" : type.toString()) +
                (output == declarations ? declarationAttributes(element) +
                    ",\"typeDescription\":" + typeRecord(type) : "") +
                (includeMethodBodies && output == declarations ? executableAttributes(path) : "") +
                (synthetic ? ",\"synthetic\":true" : "") +
                ",\"start\":" + start + ",\"end\":" + end +
                ",\"line\":" + unit.getLineMap().getLineNumber(start) +
                ",\"column\":" + unit.getLineMap().getColumnNumber(start) + "}");
        }

        private String executableAttributes(TreePath path) {
            var tree = path.getLeaf();
            if (tree instanceof MethodTree method) {
                return methodBodyRecord(path, method);
            }
            if (tree instanceof VariableTree field && field.getInitializer() != null) {
                return ",\"initializer\":" + executableExpression(new TreePath(path, field.getInitializer()));
            }
            if (tree instanceof BlockTree block) {
                var owner = trees.getElement(path.getParentPath());
                return ",\"initializerOwner\":" + quote(symbol(owner)) +
                    ",\"staticInitializer\":" + block.isStatic() +
                    ",\"body\":" + bodyRecord(path, block.getStatements());
            }
            return "";
        }

        private String methodBodyRecord(TreePath path, MethodTree method) {
            var body = method.getBody();
            if (body == null) {
                return "";
            }
            var positions = trees.getSourcePositions();
            var statements = body.getStatements();
            var bodyPath = new TreePath(path, body);
            String invocation = "";
            // Keep javac's implicit super() as well as explicit this(...) or super(...).
            if (!statements.isEmpty() && method.getReturnType() == null &&
                    statements.get(0) instanceof ExpressionStatementTree statement &&
                    statement.getExpression() instanceof MethodInvocationTree call &&
                    call.getMethodSelect() instanceof IdentifierTree selector &&
                    (selector.getName().contentEquals("this") || selector.getName().contentEquals("super"))) {
                var callPath = new TreePath(new TreePath(bodyPath, statement), call);
                invocation = ",\"constructorCall\":" + executableExpression(callPath);
                statements = statements.subList(1, statements.size());
            }
            var explicit = statements.stream()
                .filter(statement -> positions.getEndPosition(unit, statement) != Diagnostic.NOPOS ||
                    method.getReturnType() == null)
                .toList();
            return ",\"body\":" + bodyRecord(bodyPath, explicit) + invocation;
        }

        private String bodyRecord(TreePath path, List<? extends StatementTree> statements) {
            return "{\"code\":[" + statements.stream()
                .map(statement -> statementRecord(new TreePath(path, statement)))
                .collect(Collectors.joining(",")) + "]}";
        }

        private String statementRecord(TreePath path) {
            var statement = path.getLeaf();
            String record = "{\"kind\":" + quote(statement.getKind().name()) + positionRecord(statement);
            ExpressionTree expression = null;
            if (statement instanceof ReturnTree returned) {
                expression = returned.getExpression();
            } else if (statement instanceof ThrowTree thrown) {
                expression = thrown.getExpression();
            } else if (statement instanceof ExpressionStatementTree expressionStatement) {
                expression = expressionStatement.getExpression();
            } else if (statement instanceof TryTree attempted) {
                var block = attempted.getBlock();
                record += ",\"body\":" + bodyRecord(new TreePath(path, block), block.getStatements()) +
                    ",\"resources\":[" + attempted.getResources().stream()
                    .map(resource -> variableRecord(new TreePath(path, resource)))
                    .collect(Collectors.joining(",")) + "],\"catches\":[" +
                    attempted.getCatches().stream().map(handler -> catchRecord(new TreePath(path, handler), handler))
                    .collect(Collectors.joining(",")) + "]";
                var cleanup = attempted.getFinallyBlock();
                if (cleanup != null) {
                    record += ",\"finally\":" + bodyRecord(new TreePath(path, cleanup), cleanup.getStatements());
                }
            } else if (statement instanceof VariableTree) {
                record += ",\"variable\":" + variableRecord(path);
            } else if (statement instanceof BlockTree block) {
                record += ",\"body\":" + bodyRecord(path, block.getStatements());
            } else if (statement instanceof IfTree conditional) {
                expression = conditional.getCondition();
                record += ",\"then\":" + statementRecord(new TreePath(path, conditional.getThenStatement()));
                if (conditional.getElseStatement() != null) {
                    record += ",\"else\":" + statementRecord(new TreePath(path, conditional.getElseStatement()));
                }
            }
            if (expression != null) {
                record += ",\"expression\":" + executableExpression(new TreePath(path, expression));
            }
            return record + "}";
        }

        private String variableRecord(TreePath path) {
            var tree = path.getLeaf();
            if (tree instanceof VariableTree declaration) {
                var element = trees.getElement(path);
                var initializer = declaration.getInitializer();
                return "{\"name\":" + quote(declaration.getName().toString()) +
                    ",\"type\":" + quote(expressionType(element == null ? null : element.asType())) +
                    (initializer == null ? "" : ",\"initializer\":" + executableExpression(new TreePath(path, initializer))) + "}";
            }
            return "{\"type\":" + quote(expressionType(trees.getTypeMirror(path))) +
                ",\"initializer\":" + executableExpression(path) + "}";
        }

        private String catchRecord(TreePath path, CatchTree handler) {
            var parameter = handler.getParameter();
            var typePath = new TreePath(new TreePath(path, parameter), parameter.getType());
            List<? extends Tree> alternatives = parameter.getType() instanceof UnionTypeTree union ?
                union.getTypeAlternatives() : List.of(parameter.getType());
            var names = alternatives.stream().map(alternative -> {
                var memberPath = alternative == parameter.getType() ? typePath : new TreePath(typePath, alternative);
                return quote(expressionType(trees.getTypeMirror(memberPath)));
            }).collect(Collectors.joining(","));
            var body = handler.getBlock();
            return "{\"name\":" + quote(parameter.getName().toString()) + ",\"types\":[" + names +
                "],\"body\":" + bodyRecord(new TreePath(path, body), body.getStatements()) + "}";
        }

        private String positionRecord(Tree tree) {
            long start = trees.getSourcePositions().getStartPosition(unit, tree);
            if (start == Diagnostic.NOPOS) {
                return "";
            }
            return ",\"line\":" + unit.getLineMap().getLineNumber(start) +
                ",\"column\":" + unit.getLineMap().getColumnNumber(start);
        }

        private String executableExpression(TreePath path) {
            var tree = path.getLeaf();
            var type = trees.getTypeMirror(path);
            String kind = tree.getKind().name();
            String attributes = ",\"type\":" + quote(expressionType(type)) +
                positionRecord(tree);
            if (tree instanceof IdentifierTree identifier) {
                var element = trees.getElement(path);
                if (identifier.getName().contentEquals("this")) {
                    kind = "THIS";
                } else if (element != null && element.getKind() == ElementKind.PARAMETER) {
                    attributes += ",\"name\":" + quote(identifier.getName().toString());
                } else if (element != null && element.getKind() == ElementKind.EXCEPTION_PARAMETER) {
                    kind = "CATCH_PARAMETER";
                    attributes += ",\"name\":" + quote(identifier.getName().toString());
                } else if (element != null && (element.getKind() == ElementKind.LOCAL_VARIABLE ||
                        element.getKind() == ElementKind.RESOURCE_VARIABLE)) {
                    kind = "LOCAL_VARIABLE";
                    attributes += ",\"name\":" + quote(identifier.getName().toString());
                } else if (element instanceof VariableElement field && element.getKind().isField()) {
                    kind = "FIELD";
                    attributes += fieldExpression(field, "this");
                } else {
                    kind = "NON_PARAMETER_IDENTIFIER";
                }
            } else if (tree instanceof MemberSelectTree selector && trees.getElement(path) instanceof VariableElement field &&
                    field.getKind().isField()) {
                var target = new TreePath(path, selector.getExpression());
                if (selector.getIdentifier().contentEquals("length") &&
                        trees.getTypeMirror(target) instanceof ArrayType) {
                    kind = "ARRAY_LENGTH";
                    attributes += ",\"target\":" + executableExpression(target);
                } else {
                    kind = "FIELD";
                    attributes += fieldExpression(field, receiver(path, selector.getExpression()));
                }
            } else if (tree instanceof MethodInvocationTree call) {
                var callee = call.getMethodSelect();
                String receiver = callee instanceof MemberSelectTree selector ? receiver(path, selector.getExpression()) : "this";
                if (callee instanceof IdentifierTree identifier && identifier.getName().contentEquals("super")) {
                    receiver = "super";
                }
                String symbol = symbol(trees.getElement(path));
                attributes += ",\"symbol\":" + quote(symbol) + ",\"receiver\":" + quote(receiver) +
                    callArguments(path, call.getArguments());
                if (callee instanceof MemberSelectTree selector && receiver.equals("other")) {
                    var target = new TreePath(new TreePath(path, callee), selector.getExpression());
                    attributes += ",\"target\":" + executableExpression(target);
                }
            } else if (tree instanceof NewClassTree call) {
                if (call.getEnclosingExpression() == null && call.getClassBody() == null) {
                    attributes += ",\"symbol\":" + quote(symbol(trees.getElement(path))) +
                        callArguments(path, call.getArguments());
                }
            } else if (tree instanceof NewArrayTree array) {
                var initializers = array.getInitializers();
                if (initializers != null) {
                    kind = "ARRAY_INITIALIZER";
                }
                attributes += callArguments(path, initializers == null ? array.getDimensions() : initializers);
            } else if (tree instanceof ArrayAccessTree access) {
                attributes += ",\"target\":" + executableExpression(new TreePath(path, access.getExpression())) +
                    ",\"operand\":" + executableExpression(new TreePath(path, access.getIndex()));
            } else if (tree instanceof AssignmentTree assignment) {
                attributes += ",\"left\":" + executableExpression(new TreePath(path, assignment.getVariable())) +
                    ",\"right\":" + executableExpression(new TreePath(path, assignment.getExpression()));
            } else if (tree instanceof TypeCastTree cast) {
                attributes += ",\"operand\":" + executableExpression(new TreePath(path, cast.getExpression()));
            } else if (tree instanceof InstanceOfTree test && test.getPattern() == null) {
                var testedType = trees.getTypeMirror(new TreePath(path, test.getType()));
                attributes += ",\"operand\":" + executableExpression(new TreePath(path, test.getExpression())) +
                    ",\"target\":{\"kind\":\"TYPE\",\"type\":" + quote(expressionType(testedType)) + "}";
            } else if (tree instanceof ParenthesizedTree parentheses) {
                attributes += ",\"operand\":" + executableExpression(new TreePath(path, parentheses.getExpression()));
            } else if (tree instanceof UnaryTree unary) {
                attributes += ",\"operand\":" + executableExpression(new TreePath(path, unary.getExpression()));
            } else if (tree instanceof ConditionalExpressionTree conditional) {
                attributes += ",\"condition\":" + executableExpression(new TreePath(path, conditional.getCondition())) +
                    ",\"whenTrue\":" + executableExpression(new TreePath(path, conditional.getTrueExpression())) +
                    ",\"whenFalse\":" + executableExpression(new TreePath(path, conditional.getFalseExpression()));
            } else if (tree instanceof BinaryTree binary) {
                attributes += ",\"left\":" + executableExpression(new TreePath(path, binary.getLeftOperand())) +
                    ",\"right\":" + executableExpression(new TreePath(path, binary.getRightOperand()));
            } else if (tree instanceof LiteralTree literal) {
                if (literal.getValue() instanceof String text) {
                    attributes += ",\"utf16LE\":" + quote(utf16LittleEndian(text));
                } else if (type != null && type.getKind().isPrimitive()) {
                    Object value = literal.getValue();
                    String spelling = value instanceof Character character ? Integer.toString(character) : value.toString();
                    attributes += ",\"literal\":" + quote(spelling);
                }
            }
            return "{\"kind\":" + quote(kind) + attributes + "}";
        }

        private String callArguments(TreePath path, List<? extends ExpressionTree> arguments) {
            return ",\"arguments\":[" + arguments.stream()
                .map(argument -> executableExpression(new TreePath(path, argument)))
                .collect(Collectors.joining(",")) + "]";
        }

        private String expressionType(TypeMirror type) {
            if (type == null) {
                return "";
            }
            if (type instanceof DeclaredType declared && declared.asElement() instanceof TypeElement element) {
                return elements.getBinaryName(element).toString();
            }
            if (type instanceof ArrayType array) {
                return expressionType(array.getComponentType()) + "[]";
            }
            return type.getKind().name().toLowerCase(Locale.ROOT);
        }

        private String fieldExpression(VariableElement field, String receiver) {
            String attributes = ",\"symbol\":" + quote(symbol(field)) + ",\"receiver\":" + quote(receiver);
            if (field.getModifiers().contains(Modifier.STATIC)) {
                attributes += ",\"static\":true";
                Object value = field.getConstantValue();
                if (value != null && field.asType().getKind().isPrimitive()) {
                    String spelling = value instanceof Character character ? Integer.toString(character) : value.toString();
                    attributes += ",\"literal\":" + quote(spelling);
                } else if (value instanceof String text) {
                    attributes += ",\"utf16LE\":" + quote(utf16LittleEndian(text));
                }
            }
            return attributes;
        }

        private String receiver(TreePath path, ExpressionTree expression) {
            if (expression instanceof IdentifierTree name &&
                    (name.getName().contentEquals("this") || name.getName().contentEquals("super"))) {
                return name.getName().toString();
            }
            var receiverPath = TreePath.getPath(path, expression);
            return receiverPath != null && trees.getElement(receiverPath) instanceof TypeElement ? "type" : "other";
        }

        private String declarationAttributes(Element element) {
            if (element == null) {
                return "";
            }
            var enclosing = element.getEnclosingElement();
            String owner = enclosing instanceof TypeElement type ? elements.getBinaryName(type).toString() : "";
            var modifiers = element.getModifiers().stream().map(modifier -> quote(modifier.name()))
                .collect(Collectors.joining(","));
            String result = ",\"moduleName\":" + quote(moduleName(element)) +
                ",\"name\":" + quote(element.getSimpleName().toString()) +
                ",\"owner\":" + quote(owner) + ",\"modifiers\":[" + modifiers + "]" +
                ",\"annotationCount\":" + element.getAnnotationMirrors().size() +
                ",\"annotations\":[" + element.getAnnotationMirrors().stream()
                    .map(this::annotationRecord).collect(Collectors.joining(",")) + "]";
            if (element instanceof Parameterizable parameterizable) {
                var parameters = parameterizable.getTypeParameters().stream().map(parameter ->
                    "{\"name\":" + quote(parameter.getSimpleName().toString()) +
                    ",\"bounds\":" + typeList(parameter.getBounds()) + "}").collect(Collectors.joining(","));
                result += ",\"typeParameters\":[" + parameters + "]";
            }
            if (element instanceof ExecutableElement method) {
                var parameters = method.getParameters().stream().map(parameter ->
                    "{\"name\":" + quote(parameter.getSimpleName().toString()) +
                    ",\"type\":" + quote(parameter.asType().toString()) + "}").collect(Collectors.joining(","));
                var thrown = method.getThrownTypes().stream().map(type -> quote(type.toString()))
                    .collect(Collectors.joining(","));
                if (method.getDefaultValue() != null) {
                    result += ",\"annotationDefault\":" + annotationValue(method.getDefaultValue());
                }
                return result + ",\"result\":" + quote(method.getReturnType().toString()) +
                    ",\"parameters\":[" + parameters + "],\"thrown\":[" + thrown + "]" +
                    ",\"hasTypeParams\":" + !method.getTypeParameters().isEmpty();
            }
            if (element instanceof TypeElement type) {
                var supertypes = types.directSupertypes(type.asType()).stream()
                    .map(parent -> parent.toString()).filter(parent -> !parent.equals("java.lang.Object"))
                    .map(JavaSourceAnalysis::quote).collect(Collectors.joining(","));
                return result + ",\"supertypes\":[" + supertypes + "]" +
                    ",\"hasTypeParams\":" + !type.getTypeParameters().isEmpty();
            }
            if (element instanceof VariableElement variable && variable.getConstantValue() != null) {
                Object constant = variable.getConstantValue();
                if (constant instanceof String text) {
                    result += ",\"constantUtf16LE\":" + quote(utf16LittleEndian(text));
                }
                String spelling = constant instanceof Character character ?
                    Integer.toString(character) : constant.toString();
                return result + ",\"constant\":" + quote(spelling);
            }
            return result;
        }

        private String utf16LittleEndian(String value) {
            // An ASCII transport preserves unpaired surrogate code units.
            byte[] encoded = new byte[value.length() * 2];
            for (int index = 0; index < value.length(); index++) {
                char unit = value.charAt(index);
                encoded[2 * index] = (byte) unit;
                encoded[2 * index + 1] = (byte) (unit >>> 8);
            }
            return HexFormat.of().formatHex(encoded);
        }

        private String typeRecord(TypeMirror type) {
            if (type == null) {
                return "null";
            }
            String properties = "\"kind\":" + quote(type.getKind().name());
            var annotations = type.getAnnotationMirrors().stream()
                .map(this::annotationRecord).collect(Collectors.joining(","));
            properties += ",\"annotations\":[" + annotations + "]";
            if (type instanceof DeclaredType declared && declared.asElement() instanceof TypeElement element) {
                properties += ",\"name\":" + quote(symbol(element)) +
                    ",\"moduleName\":" + quote(moduleName(element)) +
                    ",\"arguments\":" + typeList(declared.getTypeArguments());
                if (declared.getEnclosingType().getKind() != TypeKind.NONE) {
                    properties += ",\"enclosing\":" + typeRecord(declared.getEnclosingType());
                }
            } else if (type instanceof ArrayType array) {
                properties += ",\"component\":" + typeRecord(array.getComponentType());
            } else if (type instanceof TypeVariable variable &&
                    variable.asElement() instanceof TypeParameterElement parameter) {
                // Bounds belong to the declaration. Including them here would
                // recurse for types such as T extends Comparable<T>.
                properties += ",\"name\":" + quote(parameter.getSimpleName().toString()) +
                    ",\"owner\":" + quote(symbol(parameter.getGenericElement()));
            } else if (type instanceof WildcardType wildcard) {
                properties += ",\"upperBound\":" + typeRecord(wildcard.getExtendsBound()) +
                    ",\"lowerBound\":" + typeRecord(wildcard.getSuperBound());
            } else if (type instanceof IntersectionType intersection) {
                properties += ",\"bounds\":" + typeList(intersection.getBounds());
            } else if (type instanceof ExecutableType executable) {
                properties += ",\"parameters\":" + typeList(executable.getParameterTypes()) +
                    ",\"result\":" + typeRecord(executable.getReturnType()) +
                    ",\"thrown\":" + typeList(executable.getThrownTypes());
            }
            return "{" + properties + "}";
        }

        private String typeList(List<? extends TypeMirror> types) {
            return "[" + types.stream().map(this::typeRecord).collect(Collectors.joining(",")) + "]";
        }

        private String annotationRecord(AnnotationMirror annotation) {
            var explicit = annotation.getElementValues();
            var members = elements.getElementValuesWithDefaults(annotation).entrySet().stream()
                .sorted(Comparator.comparing(entry -> entry.getKey().getSimpleName().toString()))
                .map(entry -> "{\"name\":" + quote(entry.getKey().getSimpleName().toString()) +
                    ",\"explicit\":" + explicit.containsKey(entry.getKey()) +
                    ",\"value\":" + annotationValue(entry.getValue()) + "}")
                .collect(Collectors.joining(","));
            var annotationType = annotation.getAnnotationType().asElement();
            return "{\"type\":" + quote(symbol(annotationType)) +
                ",\"retention\":" + quote(annotationRetention(annotationType)) +
                ",\"members\":[" + members + "]}";
        }

        private String annotationRetention(Element annotationType) {
            if (annotationType == null) {
                return "CLASS";
            }
            for (var annotation : annotationType.getAnnotationMirrors()) {
                if (annotation.getAnnotationType().toString().equals("java.lang.annotation.Retention")) {
                    for (var value : annotation.getElementValues().values()) {
                        if (value.getValue() instanceof VariableElement retention) {
                            return retention.getSimpleName().toString();
                        }
                    }
                }
            }
            return "CLASS";
        }

        private String annotationValue(AnnotationValue annotationValue) {
            var value = annotationValue.getValue();
            if (value == null) {
                return scalarValue("UNRESOLVED", "");
            }
            if (value instanceof AnnotationMirror nested) {
                return "{\"kind\":\"ANNOTATION\",\"annotation\":" + annotationRecord(nested) + "}";
            }
            if (value instanceof List<?> values) {
                var entries = values.stream().map(entry -> annotationValue((AnnotationValue) entry))
                    .collect(Collectors.joining(","));
                return "{\"kind\":\"ARRAY\",\"elements\":[" + entries + "]}";
            }
            if (value instanceof VariableElement member) {
                return scalarValue("ENUM_CONSTANT", symbol(member));
            }
            if (value instanceof TypeMirror type) {
                return scalarValue("CLASS_LITERAL", type.toString());
            }
            if (value instanceof Character character) {
                return scalarValue("CHAR", Integer.toString(character));
            }
            return scalarValue(value.getClass().getSimpleName().toUpperCase(Locale.ROOT), value.toString());
        }

        private static String scalarValue(String kind, String text) {
            return "{\"kind\":" + quote(kind) + ",\"text\":" + quote(text) + "}";
        }

        private String symbol(Element element) {
            if (element == null) {
                return "";
            }
            if (element instanceof TypeElement type) {
                return elements.getBinaryName(type).toString();
            }
            if (element instanceof ExecutableElement executable) {
                var parameters = executable.getParameters().stream()
                    .map(parameter -> types.erasure(parameter.asType()).toString())
                    .collect(Collectors.joining(","));
                return symbol(element.getEnclosingElement()) + "#" + element.getSimpleName() + "(" + parameters + ")";
            }
            return symbol(element.getEnclosingElement()) + "#" + element.getSimpleName();
        }
    }

    public static void main(String[] ignored) throws Exception {
        var arguments = new String(System.in.readAllBytes(), StandardCharsets.UTF_8).split("\0", -1);
        int release = Integer.parseInt(arguments[0]);
        boolean preview = Boolean.parseBoolean(arguments[1]);
        var options = new ArrayList<>(List.of("-proc:none", "--release", Integer.toString(release)));
        if (preview) {
            options.add("--enable-preview");
        }
        addPathOption(options, "--class-path", arguments[2]);
        addPathOption(options, "--module-path", arguments[3]);

        var compiler = ToolProvider.getSystemJavaCompiler();
        if (compiler == null) {
            System.err.println("Java source analysis requires a JDK, not a JRE");
            System.exit(2);
        }
        var diagnostics = new DiagnosticCollector<JavaFileObject>();
        var encoding = arguments[4].isEmpty() ? StandardCharsets.UTF_8 : Charset.forName(arguments[4]);
        boolean includeMethodBodies = Boolean.parseBoolean(arguments[5]);
        var sources = Arrays.stream(arguments, 6, arguments.length).map(Path::of).toList();
        var files = new ArrayList<String>();
        try (var manager = compiler.getStandardFileManager(diagnostics, Locale.ROOT, encoding)) {
            var inputs = manager.getJavaFileObjectsFromPaths(sources);
            var task = (JavacTask) compiler.getTask(null, manager, diagnostics, options, null, inputs);
            var units = task.parse();
            task.analyze();
            var trees = Trees.instance(task);
            for (var unit : units) {
                var inventory = new Inventory(unit, trees, task.getElements(), task.getTypes(), includeMethodBodies);
                inventory.scan(unit, null);
                files.add("{" + "\"path\":" + quote(filePath(unit.getSourceFile())) +
                    ",\"package\":" + quote(unit.getPackageName() == null ? "" : unit.getPackageName().toString()) +
                    moduleRecord(unit.getModule()) +
                    ",\"moduleName\":" + quote(inventory.sourceModuleName()) +
                    ",\"typeReferences\":[" + String.join(",", inventory.typeReferences.values()) + "]" +
                    ",\"declarations\":[" + String.join(",", inventory.declarations) + "]" +
                    ",\"calls\":[" + String.join(",", inventory.calls) + "]}");
            }
        }
        var diagnosticRecords = diagnostics.getDiagnostics().stream()
            .map(JavaSourceAnalysis::diagnosticRecord).collect(Collectors.joining(","));
        System.out.println("{\"schemaVersion\":1,\"release\":" + release + ",\"preview\":" + preview +
            ",\"files\":[" + String.join(",", files) + "],\"diagnostics\":[" + diagnosticRecords + "]}");
    }

    private static String moduleRecord(ModuleTree module) {
        if (module == null) {
            return "";
        }
        var directives = new ArrayList<String>();
        for (var directive : module.getDirectives()) {
            String kind = directive.getKind().name();
            if (directive instanceof RequiresTree requires) {
                directives.add("{\"kind\":" + quote(kind) +
                    ",\"name\":" + quote(requires.getModuleName().toString()) +
                    ",\"static\":" + requires.isStatic() + ",\"transitive\":" + requires.isTransitive() + "}");
            } else if (directive instanceof ExportsTree exports) {
                directives.add(moduleDirective(kind, exports.getPackageName(), exports.getModuleNames()));
            } else if (directive instanceof OpensTree opens) {
                directives.add(moduleDirective(kind, opens.getPackageName(), opens.getModuleNames()));
            } else if (directive instanceof UsesTree uses) {
                directives.add(moduleDirective(kind, uses.getServiceName(), List.of()));
            } else if (directive instanceof ProvidesTree provides) {
                directives.add(moduleDirective(kind, provides.getServiceName(), provides.getImplementationNames()));
            }
        }
        return ",\"module\":{\"name\":" + quote(module.getName().toString()) +
            ",\"open\":" + (module.getModuleType() == ModuleTree.ModuleKind.OPEN) +
            ",\"directives\":[" + String.join(",", directives) + "]}";
    }

    private static String moduleDirective(String kind, ExpressionTree name,
            List<? extends ExpressionTree> targets) {
        var values = targets == null ? "" : targets.stream().map(target -> quote(target.toString()))
            .collect(Collectors.joining(","));
        return "{\"kind\":" + quote(kind) + ",\"name\":" + quote(name.toString()) +
            ",\"targets\":[" + values + "]}";
    }

    private static void addPathOption(List<String> options, String name, String value) {
        if (!value.isEmpty()) {
            options.add(name);
            options.add(value);
        }
    }

    private static String filePath(JavaFileObject source) {
        return source == null ? "" : Path.of(source.toUri()).toString();
    }

    private static String diagnosticRecord(Diagnostic<? extends JavaFileObject> diagnostic) {
        return "{\"kind\":" + quote(diagnostic.getKind().name()) +
            ",\"code\":" + quote(diagnostic.getCode()) +
            ",\"path\":" + quote(filePath(diagnostic.getSource())) +
            ",\"line\":" + diagnostic.getLineNumber() + ",\"column\":" + diagnostic.getColumnNumber() +
            ",\"message\":" + quote(diagnostic.getMessage(Locale.ROOT)) + "}";
    }

    private static String quote(String value) {
        var result = new StringBuilder("\"");
        for (int i = 0; i < value.length(); i++) {
            char character = value.charAt(i);
            if (character == '"' || character == '\\') {
                result.append('\\').append(character);
            } else if (character < 0x20 || Character.isSurrogate(character)) {
                result.append(String.format(Locale.ROOT, "\\u%04x", (int) character));
            } else {
                result.append(character);
            }
        }
        return result.append('"').toString();
    }
}
