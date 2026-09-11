package arch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file guards ONE env var, and it exists because that var is a claim about
// the machine rather than a setting.
//
// ADR 0138 turned the testcontainers reaper off in CI. The reaper removes
// containers a test run leaked, and the whole argument for switching it off is
// that a GitHub-hosted runner is destroyed when the job ends, so there is
// nothing left to leak INTO. That argument is true of `ubuntu-latest` and false
// of a self-hosted runner, and the difference is one line in a YAML file that
// nobody would connect to a decision recorded elsewhere.
//
// So the gate is not "is the reaper off". It is: wherever the reaper is off,
// the sentence that licenses it must still hold.
//
// # What this does NOT do
//
// It does not read the runner's actual lifecycle — nothing here can. It reads
// the LABEL, and the label is trusted: `ubuntu-latest` is GitHub's and means an
// ephemeral VM. A label this file does not know is refused rather than guessed,
// which makes adding a runner a deliberate edit here instead of a silent one
// over there.
//
// It also does not model YAML. The scanner below reads indentation, because
// promoting a YAML parser to a DIRECT require would put it in the module graph
// of everybody who embeds gobit (see [TestEveryDependencyAnEmbedderInheritsIsWrittenDown]) for
// the sake of one internal test. The cost of the hand scanner is that it can
// misread a file shaped differently from the repository's; the answer to that
// is the shape assertions, which fail loudly rather than audit nothing.
const (
	// reaperDisableVar is the switch this file is about.
	reaperDisableVar = "TESTCONTAINERS_RYUK_DISABLED"

	// workflowJobFloor is the smallest number of jobs the scanner must find.
	//
	// A floor and not a count: jobs get added. What it defends against is the
	// scanner going blind — a parse that finds no jobs would make every
	// assertion below vacuously true, and this gate would pass on a workflow
	// file it never understood. The repository had four jobs when it was
	// written.
	workflowJobFloor = 4

	// reaperDisabledJobFloor is how many jobs must carry the disable.
	//
	// Zero would be the silent failure this file is here to prevent: if the
	// var is renamed upstream, or the env block is dropped in a refactor, the
	// audit would find nothing to check and pass. Two jobs run containers
	// today (Integration and Smoke) and both carry it.
	reaperDisabledJobFloor = 2
)

// ephemeralRunnerLabels are the runner labels whose machine does not outlive
// the job.
//
// These are GitHub-hosted images: the VM is created for the job and destroyed
// with it, which is the entire premise of ADR 0138. `self-hosted` is absent on
// purpose and so is every custom label — a runner somebody operates is a
// machine that keeps what a test run leaves behind.
var ephemeralRunnerLabels = map[string]bool{
	"ubuntu-latest":  true,
	"ubuntu-24.04":   true,
	"ubuntu-22.04":   true,
	"macos-latest":   true,
	"macos-15":       true,
	"macos-14":       true,
	"windows-latest": true,
	"windows-2025":   true,
	"windows-2022":   true,
}

// localSettingFiles are the file shapes that set an environment variable for a
// run somebody starts by hand.
//
// The developer's machine is the case the reaper is FOR: it survives the test
// run, so a container left behind is still there tomorrow. The names are
// matched against a walk of the whole tree rather than a list of known paths,
// so a script added later is audited the day it is added.
var localSettingFiles = map[string]bool{
	"Makefile":            true,
	".envrc":              true,
	"docker-compose.yml":  true,
	"docker-compose.yaml": true,
}

// localSettingExtensions are the same population, by extension.
var localSettingExtensions = map[string]bool{".sh": true, ".bash": true, ".env": true, ".mk": true}

// workflowJob is one job as the scanner understood it.
type workflowJob struct {
	// file and name locate the job for a failure message.
	file string
	name string
	// runsOn is the verbatim value of `runs-on:`, empty when the job has none.
	runsOn string
	// disablesReaper is true when reaperDisableVar appears anywhere in the
	// job's body — job level or step level, both of which take effect.
	disablesReaper bool
}

// TestTheReaperIsDisabledONLYOnAnEphemeralRunner holds ADR 0138 to its premise.
//
// Turning the reaper off is safe because the machine is thrown away. If a job
// that disables it ever moves to a runner somebody keeps, the containers it
// leaks are kept too — and nothing about that appears in the diff that moved
// it, because the decision lives in an ADR and the move is one word of YAML.
func TestTheReaperIsDisabledONLYOnAnEphemeralRunner(t *testing.T) {
	t.Parallel()

	jobs := scanWorkflowJobs(t)

	require.GreaterOrEqual(t, len(jobs), workflowJobFloor,
		"only %d job(s) were understood in %s; the scanner is reading a shape it was not "+
			"written for, and every assertion in this file is vacuous until it is fixed",
		len(jobs), workflowDirName)

	disabled := 0
	for _, job := range jobs {
		if !job.disablesReaper {
			continue
		}
		disabled++

		assert.True(t, ephemeralRunnerLabels[job.runsOn],
			"%s: job %q turns the reaper off but runs on %q, which is not a runner this "+
				"repository knows to be destroyed with the job. ADR 0138 switched the reaper "+
				"off BECAUSE the machine does not outlive the run; on a runner that does, the "+
				"containers the tests leak are kept. Either run this job on an ephemeral "+
				"runner, or drop the %s line, or — if the label really is ephemeral — add it "+
				"to ephemeralRunnerLabels and say so.",
			job.file, job.name, job.runsOn, reaperDisableVar)
	}

	assert.GreaterOrEqual(t, disabled, reaperDisabledJobFloor,
		"%d job(s) set %s and at least %d were expected. If the variable was renamed "+
			"upstream or an env block was dropped, this gate now audits nothing and passes "+
			"— which is exactly the silence it exists to end.",
		disabled, reaperDisableVar, reaperDisabledJobFloor)
}

// TestTheReaperIsNotDisabledOnAMachineThatKeepsWhatItLeaks refuses the switch
// outside CI.
//
// The developer's laptop is the machine the reaper was written for. A Makefile
// line that turns it off would be invisible — the lane still passes, and the
// containers pile up until something else runs out of room. Documentation may
// name the variable freely; a file that SETS things may not.
func TestTheReaperIsNotDisabledOnAMachineThatKeepsWhatItLeaks(t *testing.T) {
	t.Parallel()

	var scanned int

	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// .github is the ONE place the switch is allowed, and it is
			// audited by the test above rather than here.
			if name := entry.Name(); name == ".git" || name == ".github" || name == "node_modules" {
				return filepath.SkipDir
			}

			return nil
		}
		if !localSettingFiles[entry.Name()] && !localSettingExtensions[filepath.Ext(entry.Name())] {
			return nil
		}
		scanned++

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		assert.NotContains(t, string(body), reaperDisableVar,
			"%s sets %s, and it runs on a machine that outlives the test run. The reaper "+
				"exists for exactly that machine: what a run leaks there is still there "+
				"tomorrow. ADR 0138 turned it off on the CI runner and nowhere else.",
			path, reaperDisableVar)

		return nil
	})
	require.NoError(t, err, "the tree could not be walked")

	require.Positive(t, scanned,
		"no file that sets environment variables was found, so this gate read nothing; "+
			"%s alone should have matched", makefileName)
}

// scanWorkflowJobs reads the workflow files by indentation.
//
// The shape it assumes is the one GitHub requires: `jobs:` at the left margin,
// a job key two spaces in, and the job's body deeper than that. It deliberately
// treats the ENTIRE body as the place the variable may appear, because a
// step-level env block takes effect just as a job-level one does, and a scanner
// that only understood job level would pass a workflow that sets it in a step.
func scanWorkflowJobs(t *testing.T) []workflowJob {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(repoRoot, workflowDirName))
	require.NoError(t, err,
		"%s could not be read; if CI moved, this gate is auditing a place nothing lives in",
		workflowDirName)

	var jobs []workflowJob
	for _, entry := range entries {
		if ext := filepath.Ext(entry.Name()); ext != ".yml" && ext != ".yaml" {
			continue
		}

		path := filepath.Join(repoRoot, workflowDirName, entry.Name())
		body, readErr := os.ReadFile(path)
		require.NoError(t, readErr, "%s could not be read", path)

		jobs = append(jobs, scanOneWorkflow(entry.Name(), string(body))...)
	}

	return jobs
}

// scanOneWorkflow returns the jobs of a single workflow file.
//
// A variable set ABOVE `jobs:` is workflow level and reaches every job, so it
// is carried onto all of them rather than dropped: the dangerous reading is the
// one that lets a disable escape the audit.
func scanOneWorkflow(file, body string) []workflowJob {
	const (
		jobKeyIndent    = 2
		attributeIndent = 4
	)

	var (
		jobs          []workflowJob
		inJobs        bool
		workflowLevel bool
		current       *workflowJob
	)

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " "))

		// A line at the left margin opens or closes the job list.
		if indent == 0 {
			inJobs = trimmed == "jobs:"
			current = nil

			continue
		}

		if !inJobs {
			if strings.Contains(line, reaperDisableVar) {
				workflowLevel = true
			}

			continue
		}

		if indent == jobKeyIndent && strings.HasSuffix(trimmed, ":") {
			jobs = append(jobs, workflowJob{file: file, name: strings.TrimSuffix(trimmed, ":")})
			current = &jobs[len(jobs)-1]

			continue
		}
		if current == nil {
			continue
		}

		if indent == attributeIndent && strings.HasPrefix(trimmed, "runs-on:") {
			current.runsOn = strings.Trim(strings.TrimSpace(
				strings.TrimPrefix(trimmed, "runs-on:")), `"'`)
		}
		if strings.Contains(line, reaperDisableVar) {
			current.disablesReaper = true
		}
	}

	if workflowLevel {
		for i := range jobs {
			jobs[i].disablesReaper = true
		}
	}

	return jobs
}
