package testclusters

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScriptClusterSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("testclusters.sh requires a Unix shell")
	}
	// On macOS this exercises the bundled Bash 3.2, even if PATH has a newer Bash.
	bash := "/bin/bash"
	if _, err := os.Stat(bash); err != nil {
		t.Skip("/bin/bash is unavailable")
	}
	for _, action := range []string{"up", "down"} {
		for _, tc := range []struct {
			name string
			args []string
			want string
		}{
			{"one", []string{"os-3.9.0"}, "tdt-os-3.9.0\n"},
			{"several", []string{"os-3.9.0", "es-9.5.4"}, "tdt-es-9.5.4\ntdt-os-3.9.0\n"},
			{"unknown", []string{"missing"}, ""},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				log := filepath.Join(dir, "docker.log")
				files := map[string]string{
					"docker": `#!/bin/sh
case "$1" in
  ps) ;;
  run)
    while [ "$1" != "--name" ]; do shift; done
    printf '%s\n' "$2" >> "$TDT_TEST_LOG" ;;
  rm)
    if [ "$TDT_TEST_ACTION" = down ]; then
      printf '%s\n' "$3" >> "$TDT_TEST_LOG"
    fi ;;
  *) exit 1 ;;
esac
`,
					"curl":       "#!/bin/sh\nprintf '%s\\n' '{\"status\":\"green\",\"version\":{\"number\":\"test\"}}'\n",
					"docker.log": "",
				}
				for name, contents := range files {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0700); err != nil {
						t.Fatal(err)
					}
				}
				args := append([]string{"../../tools/testclusters.sh", action}, tc.args...)
				cmd := exec.Command(bash, args...)
				cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "TDT_TEST_LOG="+log, "TDT_TEST_ACTION="+action)
				output, err := cmd.CombinedOutput()
				wantExit := 0
				if action == "up" && tc.name == "unknown" {
					wantExit = 2
				}
				if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != wantExit {
					t.Fatalf("want exit %d, got %v: %s", wantExit, err, output)
				}
				got, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tc.want {
					t.Fatalf("clusters acted on: %q, want %q", got, tc.want)
				}
				if action == "up" && strings.Count(string(output), "ready   ") != len(tc.args) && tc.name != "unknown" {
					t.Fatalf("selected clusters were not reported ready: %s", output)
				}
			})
		}
	}
}
