//go:build ignore

// verify-integration-workflow validates .github/workflows/ci.yml using a
// structured YAML parser to ensure the integration-test job is correctly
// configured and the regular test job remains free of greenmail/integration
// settings.
//
// Run with: go run .github/scripts/verify-integration-workflow.go
package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	data, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		fatalf("read ci.yml: %v", err)
	}

	var wf map[string]any
	if err := yaml.Unmarshal(data, &wf); err != nil {
		fatalf("parse ci.yml: %v", err)
	}

	jobs := mustMap(wf, "jobs")

	checkIntegrationJob(jobs)
	checkTestJob(jobs)
	checkNonCodeNotification(jobs)

	fmt.Println("PASS: all checks passed")
}

// checkIntegrationJob verifies the integration-test job is correctly wired.
func checkIntegrationJob(jobs map[string]any) {
	job := mustMap(jobs, "integration-test")

	// needs: check-changes
	needs := asStringList(job["needs"])
	assertContains(needs, "check-changes", "integration-test.needs must include check-changes")

	// if condition references has-integration-changes
	ifCond := mustString(job, "if")
	assertContains([]string{ifCond}, "has-integration-changes", "integration-test.if must reference has-integration-changes")

	// greenmail service container
	services := mustMap(job, "services")
	gm := mustMap(services, "greenmail")
	image := mustString(gm, "image")
	assertEqual(image, "greenmail/standalone:2.1.3", "greenmail service image")

	ports := asStringList(gm["ports"])
	assertContainsSubstr(ports, "3993:3993", "greenmail must expose port 3993")
	assertContainsSubstr(ports, "3025:3025", "greenmail must expose port 3025")

	// GREENMAIL_OPTS must include fixed-user settings
	gmEnv := mustMap(gm, "env")
	opts := mustString(gmEnv, "GREENMAIL_OPTS")
	assertContains([]string{opts}, "greenmail.users=imap-test", "GREENMAIL_OPTS must register fixed user")
	assertContains([]string{opts}, "greenmail.users.login=email", "GREENMAIL_OPTS must set login=email")

	// job-level env must contain all IMAP_TEST_* vars
	env := mustMap(job, "env")
	for _, key := range []string{
		"IMAP_TEST_HOST", "IMAP_TEST_PORT",
		"IMAP_TEST_SMTP_HOST", "IMAP_TEST_SMTP_PORT",
		"IMAP_TEST_USER", "IMAP_TEST_PASS", "IMAP_TEST_MAILBOX",
	} {
		if _, ok := env[key]; !ok {
			fatalf("integration-test.env missing %s", key)
		}
	}

	// must run make test-integration
	assertStepRuns(job, "make test-integration", "integration-test must run make test-integration")

	fmt.Println("PASS: integration-test job")
}

// checkTestJob verifies the regular test job has no greenmail/integration config.
func checkTestJob(jobs map[string]any) {
	job := mustMap(jobs, "test")

	// must not have a services block
	if _, ok := job["services"]; ok {
		fatalf("test job must not have a services block (no greenmail)")
	}

	// must run make test (not make test-integration)
	steps := asList(job["steps"])
	for _, s := range steps {
		sm := toMap(s)
		run, _ := sm["run"].(string)
		if strings.Contains(run, "-tags integration") || strings.Contains(run, "test-integration") {
			fatalf("test job step %q must not run integration tests", run)
		}
	}

	assertStepRuns(job, "make test", "test job must run make test")

	fmt.Println("PASS: test job")
}

// checkNonCodeNotification verifies the notification step accounts for
// has-integration-changes so devcontainer-only PRs are not shown as "all skipped".
func checkNonCodeNotification(jobs map[string]any) {
	job, ok := jobs["non-code-only-notification"]
	if !ok {
		fmt.Println("SKIP: non-code-only-notification job not found")
		return
	}
	jm := toMap(job)
	ifCond, _ := jm["if"].(string)
	if !strings.Contains(ifCond, "has-integration-changes") {
		fatalf("non-code-only-notification.if must reference has-integration-changes to avoid false 'all skipped' display")
	}
	fmt.Println("PASS: non-code-only-notification job")
}

// helpers

func mustMap(m map[string]any, key string) map[string]any {
	v, ok := m[key]
	if !ok {
		fatalf("key %q not found", key)
	}
	res := toMap(v)
	if res == nil {
		fatalf("key %q is not a map", key)
	}
	return res
}

func toMap(v any) map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return x
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[fmt.Sprintf("%v", k)] = v
		}
		return out
	}
	return nil
}

func mustString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		fatalf("key %q not found", key)
	}
	s, ok := v.(string)
	if !ok {
		fatalf("key %q is not a string (got %T)", key, v)
	}
	return s
}

func asStringList(v any) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			out = append(out, fmt.Sprintf("%v", item))
		}
		return out
	case string:
		return []string{x}
	}
	return nil
}

func asList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

func assertContains(list []string, substr, msg string) {
	for _, s := range list {
		if strings.Contains(s, substr) {
			return
		}
	}
	fatalf("%s (substr %q not found in %v)", msg, substr, list)
}

func assertContainsSubstr(list []string, substr, msg string) {
	assertContains(list, substr, msg)
}

func assertEqual(got, want, msg string) {
	if got != want {
		fatalf("%s: want %q got %q", msg, want, got)
	}
}

func assertStepRuns(job map[string]any, runSubstr, msg string) {
	steps := asList(job["steps"])
	for _, s := range steps {
		sm := toMap(s)
		if run, _ := sm["run"].(string); strings.Contains(run, runSubstr) {
			return
		}
	}
	fatalf("%s (step with %q not found)", msg, runSubstr)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
