import com.sun.source.tree.ClassTree;
import com.sun.source.tree.CompilationUnitTree;
import com.sun.source.tree.MemberReferenceTree;
import com.sun.source.tree.MethodInvocationTree;
import com.sun.source.tree.MethodTree;
import com.sun.source.tree.NewClassTree;
import com.sun.source.tree.Tree;
import com.sun.source.tree.VariableTree;
import com.sun.source.util.JavacTask;
import com.sun.source.util.TreePathScanner;
import com.sun.source.util.Trees;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Locale;
import java.util.stream.Collectors;
import javax.lang.model.element.Element;
import javax.lang.model.element.ElementKind;
import javax.lang.model.element.ExecutableElement;
import javax.lang.model.element.TypeElement;
import javax.lang.model.element.VariableElement;
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
        final List<String> declarations = new ArrayList<>();
        final List<String> calls = new ArrayList<>();

        Inventory(CompilationUnitTree unit, Trees trees, Elements elements, Types types) {
            this.unit = unit;
            this.trees = trees;
            this.elements = elements;
            this.types = types;
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

        private void record(Tree tree, List<String> output) {
            var positions = trees.getSourcePositions();
            long start = positions.getStartPosition(unit, tree);
            long end = positions.getEndPosition(unit, tree);
            if (start == Diagnostic.NOPOS || end == Diagnostic.NOPOS) {
                return;
            }
            var path = getCurrentPath();
            var element = trees.getElement(path);
            var type = trees.getTypeMirror(path);
            output.add("{" + "\"kind\":" + quote(tree.getKind().name()) +
                ",\"symbol\":" + quote(symbol(element)) +
                ",\"type\":" + quote(type == null ? "" : type.toString()) +
                declarationAttributes(element) +
                ",\"start\":" + start + ",\"end\":" + end +
                ",\"line\":" + unit.getLineMap().getLineNumber(start) +
                ",\"column\":" + unit.getLineMap().getColumnNumber(start) + "}");
        }

        private String declarationAttributes(Element element) {
            if (element == null) {
                return "";
            }
            var enclosing = element.getEnclosingElement();
            String owner = enclosing instanceof TypeElement type ? elements.getBinaryName(type).toString() : "";
            var modifiers = element.getModifiers().stream().map(modifier -> quote(modifier.name()))
                .collect(Collectors.joining(","));
            String result = ",\"name\":" + quote(element.getSimpleName().toString()) +
                ",\"owner\":" + quote(owner) + ",\"modifiers\":[" + modifiers + "]" +
                ",\"annotationCount\":" + element.getAnnotationMirrors().size();
            if (element instanceof ExecutableElement method) {
                var parameters = method.getParameters().stream().map(parameter ->
                    "{\"name\":" + quote(parameter.getSimpleName().toString()) +
                    ",\"type\":" + quote(parameter.asType().toString()) + "}").collect(Collectors.joining(","));
                var thrown = method.getThrownTypes().stream().map(type -> quote(type.toString()))
                    .collect(Collectors.joining(","));
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
                String spelling = constant instanceof Character character ?
                    Integer.toString(character) : constant.toString();
                return result + ",\"constant\":" + quote(spelling);
            }
            return result;
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
        var sources = Arrays.stream(arguments, 4, arguments.length).map(Path::of).toList();
        var files = new ArrayList<String>();
        try (var manager = compiler.getStandardFileManager(diagnostics, Locale.ROOT, StandardCharsets.UTF_8)) {
            var inputs = manager.getJavaFileObjectsFromPaths(sources);
            var task = (JavacTask) compiler.getTask(null, manager, diagnostics, options, null, inputs);
            var units = task.parse();
            task.analyze();
            var trees = Trees.instance(task);
            for (var unit : units) {
                var inventory = new Inventory(unit, trees, task.getElements(), task.getTypes());
                inventory.scan(unit, null);
                files.add("{" + "\"path\":" + quote(filePath(unit.getSourceFile())) +
                    ",\"package\":" + quote(unit.getPackageName() == null ? "" : unit.getPackageName().toString()) +
                    ",\"declarations\":[" + String.join(",", inventory.declarations) + "]" +
                    ",\"calls\":[" + String.join(",", inventory.calls) + "]}");
            }
        }
        var diagnosticRecords = diagnostics.getDiagnostics().stream()
            .map(JavaSourceAnalysis::diagnosticRecord).collect(Collectors.joining(","));
        System.out.println("{\"schemaVersion\":1,\"release\":" + release + ",\"preview\":" + preview +
            ",\"files\":[" + String.join(",", files) + "],\"diagnostics\":[" + diagnosticRecords + "]}");
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
