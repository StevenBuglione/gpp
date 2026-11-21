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
	flags.StringVar(&options.Encoding, "encoding", "", "Java source encoding (default: UTF-8)")
	flags.BoolVar(&options.Preview, "enable-preview", false, "enable previews for the selected release")
	flags.StringVar(&options.ClassPath, "class-path", "", "Java compiler classpath")
	flags.StringVar(&options.ModulePath, "module-path", "", "Java compiler module path")
	flags.StringVar(&options.Java, "java", "java", "Java launcher from a JDK")
	analyze := flags.Bool("analyze", false, "emit resolved symbols, calls, and diagnostics as JSON")
	manifest := flags.String("inputs", "", "Java build inputs manifest")
	emit := flags.Bool("emit-gpp", false, "emit GPP for supported Java source")
	output := flags.String("out-dir", "", "write one GPP compilation unit per Java package")
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
	if *output != "" && !*emit {
		fatalf("--out-dir requires --emit-gpp")
		return 2
	}
	sources := flags.Args()
	if *manifest != "" {
		var inputOverrides []string
		flags.Visit(func(option *flag.Flag) {
			if slices.Contains([]string{"release", "encoding", "enable-preview", "class-path", "module-path"}, option.Name) {
				inputOverrides = append(inputOverrides, "--"+option.Name)
			}
		})
		if len(sources) != 0 || len(inputOverrides) != 0 {
			fatalf("--inputs supplies all Java sources, compiler paths, release, preview setting, and encoding")
			return 2
		}
		inputs, err := javaport.ReadBuildInputs(*manifest)
		if err != nil {
			return reportError(err)
		}
		options.Release = inputs.Release
		options.Encoding = inputs.Encoding
		options.Preview = inputs.Preview
		options.ClassPath = strings.Join(inputs.ClassPath, string(filepath.ListSeparator))
		options.ModulePath = strings.Join(inputs.ModulePath, string(filepath.ListSeparator))
		sources = inputs.Sources
	}
	if len(sources) == 0 {
		fatalf("expected one or more Java source files or directories")
		return 2
	}
	options.MethodBodies = *emit
	report, err := javaport.Analyze(context.Background(), sources, options)
	if err != nil {
		return reportError(err)
	}
	if *emit {
		if *output != "" {
			packages, err := javaport.EmitGPPPackages(report)
			if err != nil {
				return reportError(err)
			}
			if err := writeJavaPackages(*output, packages); err != nil {
				return reportError(err)
			}
			return 0
		}
		source, err := javaport.EmitGPP(report)
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

func writeJavaPackages(destination string, packages []javaport.EmittedPackage) error {
	destination, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("Java output directory already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".gpp-java-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	for _, pkg := range packages {
		directory := filepath.Join(staging, filepath.FromSlash(strings.ReplaceAll(pkg.Name, ".", "/")))
		if err := os.MkdirAll(directory, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(directory, "translated.gpp"), []byte(pkg.Source), 0644); err != nil {
			return err
		}
	}
	if err := os.Chmod(staging, 0755); err != nil {
		return err
	}
	if err := os.Rename(staging, destination); err != nil {
		return fmt.Errorf("publish generated Java packages: %w", err)
	}
	return nil
}
