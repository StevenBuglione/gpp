import java.io.IOException;
import java.lang.annotation.Retention;
import java.lang.annotation.RetentionPolicy;
import java.util.Map;

import org.springframework.boot.Banner;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.WebApplicationType;
import org.springframework.boot.SpringBootConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.boot.diagnostics.FailureAnalysis;
import org.springframework.context.annotation.Bean;
import org.springframework.core.annotation.AliasFor;
import org.springframework.core.annotation.AnnotatedElementUtils;
import org.springframework.transaction.TransactionDefinition;
import org.springframework.transaction.annotation.EnableTransactionManagement;
import org.springframework.transaction.annotation.Transactional;
import org.springframework.transaction.support.AbstractPlatformTransactionManager;
import org.springframework.transaction.support.DefaultTransactionStatus;

public class SpringBootProbe {
    @Retention(RetentionPolicy.RUNTIME)
    @interface Label {
        String value();
    }

    @Retention(RetentionPolicy.RUNTIME)
    @Label("default")
    @interface Named {
        @AliasFor(annotation = Label.class, attribute = "value")
        String label();
    }

    record Token<T>(T value) {}
    record Injected(String text, int number) {}
    record Dependency() {}
    record Consumer(Dependency dependency) {}
    record Present() {}
    record Missing() {}

    static class CountingTransactionManager extends AbstractPlatformTransactionManager {
        int commits;
        int rollbacks;

        @Override
        protected Object doGetTransaction() {
            return new Object();
        }

        @Override
        protected void doBegin(Object transaction, TransactionDefinition definition) {}

        @Override
        protected void doCommit(DefaultTransactionStatus status) {
            commits++;
        }

        @Override
        protected void doRollback(DefaultTransactionStatus status) {
            rollbacks++;
        }
    }

    static class Work {
        @Transactional
        public void checked() throws IOException {
            throw new IOException("checked");
        }

        @Transactional
        public void unchecked() {
            throw new IllegalStateException("unchecked");
        }

        @Transactional(rollbackFor = IOException.class)
        public void checkedWithRollback() throws IOException {
            throw new IOException("checked with explicit rollback");
        }
    }

    @EnableTransactionManagement
    @SpringBootConfiguration
    @Named(label = "spring-gpp")
    static class Configuration {
        @Bean
        CountingTransactionManager transactionManager() {
            return new CountingTransactionManager();
        }

        @Bean
        Work work() {
            return new Work();
        }

        @Bean
        Token<String> word() {
            return new Token<>("GPP");
        }

        @Bean
        Token<Integer> count() {
            return new Token<>(42);
        }

        @Bean
        Injected injected(Token<String> firstInput, Token<Integer> secondInput) {
            return new Injected(firstInput.value(), secondInput.value());
        }

        @Bean
        Dependency dependency() {
            return new Dependency();
        }

        @Bean
        Consumer consumer() {
            return new Consumer(dependency());
        }

        @Bean
        @ConditionalOnClass(name = "java.lang.Record")
        Present present() {
            return new Present();
        }

        @Bean
        @ConditionalOnClass(name = "gpp.reference.AbsentOptionalDependency")
        Missing missing() {
            return new Missing();
        }
    }

    public static void main(String[] args) {
        var description = new String(new char[]{0x0069, 0x0000, 0xd800});
        var action = new String("check the connection");
        var cause = new IllegalArgumentException("invalid parameter");
        var analysis = new FailureAnalysis(description, action, cause);
        require(analysis.getDescription() == description && analysis.getAction() == action && analysis.getCause() == cause,
            "failure-analysis input identity");
        var unnamed = new FailureAnalysis(null, null, null);
        require(unnamed.getDescription() == "" && unnamed.getAction() == null && unnamed.getCause() == null,
            "failure-analysis null inputs");

        var application = new SpringApplication(Configuration.class);
        application.setWebApplicationType(WebApplicationType.NONE);
        application.setBannerMode(Banner.Mode.OFF);
        application.setLogStartupInfo(false);
        application.setDefaultProperties(Map.of("gpp.probe.config", "default"));
        try (var context = application.run("--gpp.probe.config=override")) {
            var annotation = AnnotatedElementUtils.findMergedAnnotation(Configuration.class, Label.class);
            require(annotation != null && annotation.value().equals("spring-gpp"), "annotation alias");
            require(context.getBean(Injected.class).equals(new Injected("GPP", 42)), "generic injection");
            require(context.getBean(Consumer.class).dependency() == context.getBean(Dependency.class),
                "configuration proxy identity");
            require(context.containsBean("present") && !context.containsBean("missing"), "class conditions");
            require("override".equals(context.getEnvironment().getProperty("gpp.probe.config")),
                "configuration precedence");
            verifyTransactions(context.getBean(Work.class), context.getBean(CountingTransactionManager.class));
            System.out.println("GPP_SPRING_BOOT_REFERENCE=alias,generics,identity,conditions,configuration,rollback");
        }
    }

    private static void verifyTransactions(Work work, CountingTransactionManager manager) {
        try {
            work.checked();
            throw new AssertionError("expected checked exception");
        } catch (IOException expected) {
            require(manager.commits == 1 && manager.rollbacks == 0, "checked exception commits");
        }
        try {
            work.unchecked();
            throw new AssertionError("expected unchecked exception");
        } catch (IllegalStateException expected) {
            require(manager.commits == 1 && manager.rollbacks == 1, "unchecked exception rolls back");
        }
        try {
            work.checkedWithRollback();
            throw new AssertionError("expected checked exception with rollback rule");
        } catch (IOException expected) {
            require(manager.commits == 1 && manager.rollbacks == 2, "explicit checked rollback rule");
        }
    }

    private static void require(boolean condition, String contract) {
        if (!condition) {
            throw new AssertionError("Spring Boot reference failed: " + contract);
        }
    }
}
