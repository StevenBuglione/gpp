package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/telgatech/gpp/internal/javaport"
)

func runMigrate(args []string) int {
	if wantsHelp(args) {
		return commandHelp("migrate")
	}
	if len(args) == 0 || args[0] != "java" {
		fatalf("expected gpp migrate java; run gpp help migrate for usage")
		return 2
	}
	options := javaport.Options{}
	flags := flag.NewFlagSet("gpp migrate java", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() { commandHelp("migrate") }
	flags.IntVar(&options.Release, "release", 17, "Java source release")
	flags.BoolVar(&options.Preview, "enable-preview", false, "enable previews for the selected release")
	flags.StringVar(&options.ClassPath, "class-path", "", "Java compiler classpath")
	flags.StringVar(&options.ModulePath, "module-path", "", "Java compiler module path")
	flags.StringVar(&options.Java, "java", "java", "Java launcher from a JDK")
	analyze := flags.Bool("analyze", false, "emit resolved symbols, calls, and diagnostics as JSON")
	manifest := flags.String("inputs", "", "Java build inputs manifest")
	emit := flags.Bool("emit-gpp", false, "emit GPP for supported primitive-only Java interfaces")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *analyze == *emit {
		fatalf("select exactly one of --analyze and --emit-gpp")
		return 2
	}
	sources := flags.Args()
	if *manifest != "" {
		var inputOverrides []string
		flags.Visit(func(option *flag.Flag) {
			if slices.Contains([]string{"release", "enable-preview", "class-path", "module-path"}, option.Name) {
				inputOverrides = append(inputOverrides, "--"+option.Name)
			}
		})
		if len(sources) != 0 || len(inputOverrides) != 0 {
			fatalf("--inputs supplies all Java sources, release, preview setting, and paths")
			return 2
		}
		inputs, err := javaport.ReadBuildInputs(*manifest)
		if err != nil {
			return reportError(err)
		}
		options.Release = inputs.Release
		options.Preview = inputs.Preview
		options.ClassPath = strings.Join(inputs.ClassPath, string(filepath.ListSeparator))
		options.ModulePath = strings.Join(inputs.ModulePath, string(filepath.ListSeparator))
		sources = inputs.Sources
	}
	if len(sources) == 0 {
		fatalf("expected one or more Java source files or directories")
		return 2
	}
	report, err := javaport.Analyze(context.Background(), sources, options)
	if err != nil {
		return reportError(err)
	}
	if *emit {
		source, err := javaport.EmitInterfaces(report)
		if err != nil {
			return reportError(err)
		}
		if _, err := fmt.Fprint(os.Stdout, source); err != nil {
			return reportError(err)
		}
		return 0
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return reportError(err)
	}
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Kind == "ERROR" {
			return 1
		}
	}
	return 0
}
