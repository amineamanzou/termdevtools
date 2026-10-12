package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/gnocent/termdevtools/i18n"
)

type cliOptions struct {
	ContextName, ExportDefaults string
	ShowVersion                 bool
}

func parseCLI(args []string) (cliOptions, error) {
	for _, arg := range args {
		if arg == "--version" || arg == "-version" {
			return cliOptions{ShowVersion: true}, nil
		}
	}
	var options cliOptions
	msgs := i18n.For("")
	set := flag.NewFlagSet("termdevtools", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&options.ContextName, "context", "", msgs.UsageContext)
	set.StringVar(&options.ExportDefaults, "export-defaults", "", msgs.UsageExportDefaults)
	set.BoolVar(&options.ShowVersion, "version", false, msgs.UsageVersion)
	if err := set.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if set.NArg() != 0 {
		return cliOptions{}, fmt.Errorf("unexpected positional argument")
	}
	var empty bool
	set.Visit(func(f *flag.Flag) {
		if (f.Name == "context" || f.Name == "export-defaults") && f.Value.String() == "" {
			empty = true
		}
	})
	if empty {
		return cliOptions{}, fmt.Errorf("--context and --export-defaults require non-empty values")
	}
	if options.ContextName != "" && options.ExportDefaults != "" {
		return cliOptions{}, fmt.Errorf("--context and --export-defaults cannot be used together")
	}
	return options, nil
}
