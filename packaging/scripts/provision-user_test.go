// SPDX-License-Identifier: Apache-2.0

package scripts

import (
	"os"
	"strings"
	"testing"
)

type provisionedDir struct {
	owner string
	group string
	mode  string
}

func TestProvisionUserScript_SeparatesAgentAndHelperWritePaths(t *testing.T) {
	// This test deliberately inspects the provisioning declaration instead of
	// executing a root-only script. It therefore runs without root privileges.
	script, err := os.ReadFile("provision-user.sh")
	if err != nil {
		t.Fatalf("reading provision-user.sh: %v", err)
	}

	dirs := parseInstalledDirs(t, string(script))
	want := map[string]provisionedDir{
		"/var/lib/bitacora":                {owner: "bitacora", group: "bitacora", mode: "0750"},
		"/var/lib/bitacora/spool":          {owner: "root", group: "bitacora", mode: "0750"},
		"/var/lib/bitacora/spool/outbound": {owner: "bitacora", group: "bitacora", mode: "0750"},
	}

	for path, expected := range want {
		if got, ok := dirs[path]; !ok {
			t.Errorf("%s is not provisioned", path)
		} else if got != expected {
			t.Errorf("%s = %#v, want %#v", path, got, expected)
		}
	}
}

func parseInstalledDirs(t *testing.T, script string) map[string]provisionedDir {
	t.Helper()
	dirs := make(map[string]provisionedDir)
	for _, line := range strings.Split(script, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 9 || fields[0] != "install" || fields[1] != "-d" ||
			fields[2] != "-o" || fields[4] != "-g" || fields[6] != "-m" {
			continue
		}
		dirs[fields[8]] = provisionedDir{
			owner: fields[3],
			group: fields[5],
			mode:  fields[7],
		}
	}
	return dirs
}
