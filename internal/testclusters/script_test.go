package testclusters

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestScriptClusterSelection runs cluster selection on macOS Bash 3.2 without
// starting or removing real containers.
func TestScriptClusterSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("testclusters.sh requires a Unix shell")
	}
	// On macOS this exercises the bundled Bash 3.2, even if PATH has a newer Bash.
	bash := "/bin/bash"
	if _, err := os.Stat(bash); err != nil {
		t.Skip("/bin/bash is unavailable")
	}
	first, last := defaults[0].Name, defaults[len(defaults)-1].Name
	for _, action := range []string{"up", "down"} {
		unknownExit := 0
		if action == "up" {
			unknownExit = 2
		}
		for _, tc := range []struct {
			name      string
			args      []string
			want      string
			wantExit  int
			wantReady int
		}{
			{"one", []string{last}, "tdt-" + last + "\n", 0, 1},
			{"several", []string{last, first}, "tdt-" + first + "\ntdt-" + last + "\n", 0, 2},
			{"unknown", []string{"missing"}, "", unknownExit, 0},
		} {
			t.Run(action+"/"+tc.name, func(t *testing.T) {
				dir := t.TempDir()
				log := filepath.Join(dir, "docker.log")
				files := map[string]string{
					"docker": `#!/bin/sh
case "$1" in
  __tdt_test_probe__) printf tdt-fake-docker ;;
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
				if err := verifyScriptDocker(bash, cmd.Env, filepath.Join(dir, "docker")); err != nil {
					t.Skip(err)
				}
				output, err := cmd.CombinedOutput()
				if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.wantExit {
					t.Fatalf("want exit %d, got %v: %s", tc.wantExit, err, output)
				}
				got, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tc.want {
					t.Fatalf("clusters acted on: %q, want %q", got, tc.want)
				}
				if action == "up" && strings.Count(string(output), "ready   ") != tc.wantReady {
					t.Fatalf("selected clusters were not reported ready: %s", output)
				}
			})
		}
	}
}

// TestScriptBash32Compatible keeps Bash 4-only constructs from returning, even
// when the behavior tests run on Linux or are skipped on Windows.
func TestScriptBash32Compatible(t *testing.T) {
	data, err := os.ReadFile("../../tools/testclusters.sh")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := regexp.MustCompile(`\b(mapfile|readarray)\b|\bdeclare\s+-[A-Za-z]*A|\$\{[^}]*?(,,|\^\^)`)
	for n, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if forbidden.MatchString(line) {
			t.Errorf("line %d uses syntax unavailable in Bash 3.2: %s", n+1, line)
		}
	}
}

// TestScriptDockerGuardRejectsFallback ensures an unusable test double cannot
// send the cluster script's commands to another Docker executable on PATH.
func TestScriptDockerGuardRejectsFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix shell")
	}
	dir := t.TempDir()
	unusable := filepath.Join(dir, "docker")
	if err := os.WriteFile(unusable, []byte("#!/bin/sh\nexit 0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fallback := t.TempDir()
	log := filepath.Join(fallback, "called")
	if err := os.WriteFile(filepath.Join(fallback, "docker"), []byte(fmt.Sprintf("#!/bin/sh\ntouch %q\n", log)), 0700); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+fallback+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := verifyScriptDocker("/bin/bash", env, unusable); err == nil {
		t.Error("accepted fallback Docker executable")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Error("executed fallback Docker during preflight")
	}
}

// verifyScriptDocker checks resolution and execution before the script can run
// destructive Docker commands. Executing the absolute fake path also catches
// noexec filesystems without letting Bash fall back to another Docker binary.
func verifyScriptDocker(bash string, env []string, fake string) error {
	resolve := exec.Command(bash, "-c", "command -v docker")
	resolve.Env = env
	output, err := resolve.Output()
	if err != nil || strings.TrimSpace(string(output)) != fake {
		return fmt.Errorf("test Docker is not selected by the shell: %v", err)
	}
	probe := exec.Command(fake, "__tdt_test_probe__")
	probe.Env = env
	output, err = probe.Output()
	if err != nil || string(output) != "tdt-fake-docker" {
		return fmt.Errorf("test Docker cannot execute: %v", err)
	}
	return nil
}
