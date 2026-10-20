// Command termdevtools is a terminal-mode Elasticsearch client, inspired by
// Kibana's DevTools view. See SPEC.md for the detail of design choices.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/rivo/tview"

	"github.com/gnocent/termdevtools/config"
	"github.com/gnocent/termdevtools/i18n"
	"github.com/gnocent/termdevtools/refdata"
	"github.com/gnocent/termdevtools/ui"
)

// version identifies the build. Release binaries and the install scripts
// stamp it (-ldflags "-X main.version=..."); otherwise buildVersion falls
// back on what Go recorded in the binary.
var version = "dev"

// buildVersion is what --version prints: the stamped version if there is
// one, else the main module's version from the build info — the one asked
// for by "go install github.com/gnocent/termdevtools@v0.7.2", or derived
// from git by a plain "go build" in a checkout — else "dev".
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func main() {
	options, err := parseCLI(os.Args[1:])
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stdout, "Usage: termdevtools [--context NAME] [--export-defaults DIR] [--version]")
		return
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if options.ShowVersion {
		fmt.Println("termdevtools " + buildVersion())
		return
	}

	cfg, err := config.Load()
	if err != nil {
		// cfg (and so cfg.Language) isn't available yet at this point —
		// i18n.For("") gives the interface's default language, English.
		fmt.Fprintf(os.Stderr, i18n.For("").ErrConfigLoadFmt+"\n", err)
		os.Exit(1)
	}
	msgs := i18n.For(cfg.Language)

	exeDir, err := config.ExecutableDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, msgs.ErrExecDirFmt+"\n", err)
		os.Exit(1)
	}
	// Where the user's reference files are read from, and where exports and
	// crash reports are written. The error is not checked again: config.Load
	// above resolves the same directory and has already failed without it.
	userDir, _ := config.ConfigDir()
	reference := refdata.Sources{TeamDir: exeDir, UserDir: userDir}

	if options.ExportDefaults != "" {
		// Exported where reference files are read from, the built-in recipes
		// would all come back as the user's own: frozen copies, hiding the
		// corrections of every later version.
		if reference.Reads(options.ExportDefaults) {
			fmt.Fprintf(os.Stderr, msgs.ErrExportIntoSourceFmt+"\n", options.ExportDefaults)
			os.Exit(1)
		}
		written, err := refdata.ExportDefaults(options.ExportDefaults)
		if err != nil {
			fmt.Fprintf(os.Stderr, msgs.ErrExportDefaultsFmt+"\n", err)
			os.Exit(1)
		}
		fmt.Printf(msgs.InfoExportedDefaultsFmt+"\n", len(written), options.ExportDefaults)
		return
	}

	// Best-effort, like the other self-documenting files (config.yaml,
	// variables): without it the user's recipes simply have no template.
	_ = refdata.CreateUserTemplate(userDir)
	paths := buildPaths(exeDir, userDir, reference)

	tapp := tview.NewApplication()
	defer recoverCrash(tapp, userDir, msgs)
	contextsPath, err := config.ContextsPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	inventory, err := config.LoadContexts(contextsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	controller := ui.NewSessionController(ui.ControllerOptions{App: tapp, Config: cfg, Inventory: inventory, ContextsPath: contextsPath, Paths: paths})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sig)
	signalDone := make(chan struct{})
	defer close(signalDone)
	go func() {
		select {
		case <-sig:
			controller.RequestStop(true)
		case <-signalDone:
		}
	}()
	if err := controller.Run(options.ContextName); err != nil {
		fmt.Fprintf(os.Stderr, msgs.ErrFatalFmt+"\n", err)
		os.Exit(1)
	}

}

// buildPaths resolves the files the interface reads and writes (SPEC.md
// §9.1). exeDir, the binary's directory, only supplies what an installation
// shares and is read; what the program writes — exports — goes to userDir,
// the user's own configuration directory: in a shared or read-only
// installation the binary's directory can't be written to.
func buildPaths(exeDir, userDir string, reference refdata.Sources) ui.Paths {
	return ui.Paths{
		Cheatsheet: filepath.Join(exeDir, ui.CheatsheetFileName),
		Starter:    refdata.Starter(),
		Exports:    filepath.Join(userDir, ui.ExportsDirName),
		Reference:  reference,
	}
}

// recoverCrash catches a panic in the main goroutine — where
// tview.Application.Run processes events — and writes it, with a full stack
// trace, to a timestamped file in dir, the user's configuration directory:
// the only way to get a real diagnosis for a crash nobody watching can
// reproduce or transcribe by hand. Best-effort only: a panic inside tcell's
// own separate input-reading goroutine, rather than in the application code
// Run() itself processes, would not be caught here — Go's recover only
// catches panics in the same goroutine as the deferred call.
func recoverCrash(tapp *tview.Application, dir string, msgs *i18n.Strings) {
	r := recover()
	if r == nil {
		return
	}

	// Best-effort attempt to restore the terminal before printing anything —
	// guarded so a second panic here (screen state may already be broken)
	// doesn't hide the original crash report below.
	func() {
		defer func() { _ = recover() }()
		tapp.Stop()
	}()

	report := fmt.Sprintf("%v\n\n%s", r, debug.Stack())

	fmt.Fprintf(os.Stderr, msgs.ErrCrashedFmt+"\n", r)
	if path, err := writeCrashReport(dir, report, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, msgs.ErrCrashReportWriteFailedFmt+"\n", err)
		fmt.Fprint(os.Stderr, report)
	} else {
		fmt.Fprintf(os.Stderr, msgs.InfoCrashReportFmt+"\n", path)
	}
	os.Exit(1)
}

// writeCrashReport writes report to a timestamped file in dir — created if
// needed: nothing guarantees it still exists by the time a crash happens —
// and returns the path of that file.
func writeCrashReport(dir, report string, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("crash-%s.log", now.Format("20060102-150405")))
	if err := os.WriteFile(path, []byte(report), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
