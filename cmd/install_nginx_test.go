package cmd

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ABT-Tech-Limited/ft/internal/dpkgx"
	"github.com/ABT-Tech-Limited/ft/internal/installer"
)

func TestInstallNginxConfiguresDefaultsLast(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh", true: "installed"}[installed], func(t *testing.T) {
			var statuses map[string]dpkgx.Status
			if installed {
				statuses = installedStatuses(installer.Nginx().RequiredPackages)
			}
			recorder := newInstallRecorder(statuses)
			output, err := executeInstall(t, recorder, "nginx")
			if err != nil {
				t.Fatal(err)
			}
			want := recordedCommands(installer.Nginx().Steps(installed))
			want = append(want, recordedInstallCommand{argv: []string{"/usr/local/bin/ft", "nginx", "configure"}})
			if !reflect.DeepEqual(recorder.commands, want) {
				t.Errorf("commands = %#v, want %#v", recorder.commands, want)
			}
			if !strings.Contains(output, "Configuring Nginx defaults and Cloudflare refresh") {
				t.Errorf("missing configuration step: %s", output)
			}
		})
	}
}

func TestInstallNginxConfigurationFailureDoesNotReportSuccess(t *testing.T) {
	recorder := newInstallRecorder(nil)
	recorder.runErrorAt = len(installer.Nginx().Steps(false))
	wantErr := errors.New("configuration rejected")
	recorder.runErr = wantErr
	output, err := executeInstall(t, recorder, "nginx")
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if strings.Contains(output, "installed successfully") || strings.Contains(output, "Nginx is ready") {
		t.Errorf("reported success after configuration failure: %s", output)
	}
}
