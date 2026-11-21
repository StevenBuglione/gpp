package consumer

import (
	"errors"
	"slices"
	"testing"

	"github.com/StevenBuglione/spring-boot-gpp/org/springframework/boot/diagnostics"
	"github.com/StevenBuglione/spring-framework-gpp/gpp/java"
	"github.com/StevenBuglione/spring-framework-gpp/org/springframework/jdbc/core/metadata"
)

func TestFailureAnalysisSharesFrameworkJavaRuntime(t *testing.T) {
	name := java.StringFromUTF16([]uint16{0x0069, 0x0000, 0xd800})
	parameter := metadata.NewTableParameterMetaDataWithStringAndIntAndBoolean(name, 12, true)
	action := java.StringFromGo("check the connection")
	cause := &java.IllegalArgumentException{Message: "invalid parameter"}
	analysis := diagnostics.NewFailureAnalysis(parameter.GetParameterName(), action, cause)
	if analysis.GetDescription() != name || analysis.GetAction() != action || analysis.GetCause() != cause {
		t.Fatal("Boot changed the identity of an input from the Framework runtime")
	}
	var typedCause *java.IllegalArgumentException
	if !errors.As(analysis.GetCause(), &typedCause) || typedCause != cause {
		t.Fatal("Boot's cause is unavailable to Go as the original Java error")
	}
	if !slices.Equal(analysis.GetDescription().UTF16(), []uint16{0x0069, 0x0000, 0xd800}) {
		t.Fatal("Boot changed the description's UTF-16 units")
	}
	unnamed := diagnostics.NewFailureAnalysis(nil, nil, nil)
	if unnamed.GetDescription() != java.InternStringLiteral("") || unnamed.GetAction() != nil || unnamed.GetCause() != nil {
		t.Fatal("Boot did not use the Framework's interned empty String and preserve nullable inputs")
	}

	var analyzer diagnostics.FailureAnalyzer = diagnosticAnalyzer(func(failure java.Throwable) *diagnostics.FailureAnalysis {
		if failure != cause {
			return nil
		}
		return analysis
	})
	reports := 0
	var reporter diagnostics.FailureAnalysisReporter = diagnosticReporter(func(report *diagnostics.FailureAnalysis) {
		reports++
		if report != analysis || report.GetCause() != cause {
			t.Fatal("native Go reporter lost the original analysis or cause")
		}
	})
	reporter.Report(analyzer.Analyze(cause))
	if reports != 1 || analyzer.Analyze(&java.NullPointerException{}) != nil {
		t.Fatal("native Go analyzer must support a nullable analysis result")
	}
}

type diagnosticAnalyzer func(java.Throwable) *diagnostics.FailureAnalysis

func (analyzer diagnosticAnalyzer) Analyze(failure java.Throwable) *diagnostics.FailureAnalysis {
	return analyzer(failure)
}

type diagnosticReporter func(*diagnostics.FailureAnalysis)

func (reporter diagnosticReporter) Report(analysis *diagnostics.FailureAnalysis) {
	reporter(analysis)
}
