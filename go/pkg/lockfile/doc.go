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
// Their Strict variants return an error when the ref is invalid, so callers can
// fail closed instead of skipping the value.
//
// The text after "@" is parsed exactly once, by [ParsePinRef], into a
// [PinRef]: either a full commit SHA ([CommitID]) or a validated tag or branch
// name ([RefName]). A dependency's recorded ref ([Action].Ref) is always a
// [RefName]. Resolve a RefName only by exact lookup of [RefName.TagRef] or
// [RefName.BranchRef]; never pass ref text to anything that evaluates
// revision expressions.
package lockfile
