// Package lockfile parses the GitHub Actions dependency lockfile format.
//
// [Parse] is the entry point: it takes the bytes of
// .github/workflows/actions.lock and returns a [File] whose Workflows and
// Dependencies maps resolve every pinned action for a workflow.
//
// [ParseActionRef] parses a single workflow `uses:` string into its
// owner/repo/ref components, returning nil for anything that is not a
// repository action. [ParseReusableWorkflowRef] is its mirror for the
// reusable-workflow shape (owner/repo/.github/workflows/name.yml@ref);
// [IsLocalReusableWorkflow] handles the local ./.github/workflows/... shape.
package lockfile
