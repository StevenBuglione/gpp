package port;

import java.net.URL;
import java.net.URLClassLoader;
import java.nio.file.Path;

public class Probe {
    public static int run(String jar, long handle) throws ReflectiveOperationException, java.io.IOException {
        URL[] classPath = {Path.of(jar).toUri().toURL()};
        var parent = GoBackedBase.class.getClassLoader();
        try (var first = new URLClassLoader(classPath, parent);
             var second = new URLClassLoader(classPath, parent)) {
            var firstClass = first.loadClass("extension.Extension");
            var secondClass = second.loadClass("extension.Extension");
            if (firstClass == secondClass || !firstClass.getName().equals(secondClass.getName())) {
                throw new AssertionError("extension classes lost loader-qualified identity");
            }
            var left = (GoBackedBase) firstClass.getConstructor(long.class).newInstance(handle);
            var right = (GoBackedBase) secondClass.getConstructor(long.class).newInstance(handle);
            if (left.identity() != left || right.identity() != right || left == right) {
                throw new AssertionError("Java object identity changed");
            }
            return left.value() + right.value();
        }
    }
}
