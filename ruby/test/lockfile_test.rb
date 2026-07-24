# frozen_string_literal: true

require "json"
require "minitest/autorun"
require "actions/lockfile"

class LockfileTest < Minitest::Test
  Lockfile = GitHub::Actions::Lockfile
  FIXTURE = File.expand_path("../../testdata/actions-lock-v0.0.2.yml", __dir__)
  VALID_ACTION = {
    "ref" => "v4",
    "commit" => "sha1-11bd71901bbe5b1630ceea73d27597364c9af683",
    "owner_id" => 1,
    "repo_id" => 2
  }.freeze

  def test_parses_shared_fixture_and_looks_up_workflow_and_pin
    lockfile = Lockfile.parse(File.read(FIXTURE))

    assert_equal "v0.0.2", lockfile.version
    assert_equal ["actions/checkout@v4.3.1"], lockfile.lookup_workflow(".github/workflows/ci.yml")
    assert_equal "v4.3.1", lockfile.lookup_pin("Actions", "Checkout", "v4.3.1").ref
    assert_nil lockfile.lookup_workflow(".github/workflows/missing.yml")
  end

  def test_dependency_validation_matches_root_schema
    schema = JSON.parse(File.read(File.expand_path("../../schema/lockfile-v0.0.2.json", __dir__)))
    action_schema = schema.fetch("$defs").fetch("action")

    assert_equal action_schema.fetch("required").sort, Lockfile::REQUIRED_ACTION_KEYS.sort
    assert_equal action_schema.fetch("properties").keys.sort, Lockfile::ACTION_KEYS.sort
  end

  def test_rejects_missing_fields_bad_digest_and_unknown_fields
    [
      VALID_ACTION.reject { |key| key == "ref" },
      VALID_ACTION.merge("commit" => "sha1-nope"),
      VALID_ACTION.merge("flavor" => "spicy")
    ].each do |action|
      assert_raises(Lockfile::ParseError) { parse_with(action) }
    end
  end

  def test_rejects_unsafe_workflow_paths
    ["../../../etc/passwd", "/etc/shadow", "C:/Windows/system.ini", "..\\secret"].each do |path|
      error = assert_raises(Lockfile::ParseError) do
        Lockfile.parse("version: v0.0.2\nworkflows:\n  #{path.inspect}: []\n")
      end
      assert_match(/workflow path/, error.message)
    end
  end

  def test_wraps_all_safe_load_errors_as_parse_errors
    [
      ["version: [\n", Psych::SyntaxError],
      ["--- !ruby/object:Object {}\n", Psych::DisallowedClass],
      ["version: v0.0.2\nworkflows: &workflows {}\ndependencies: *workflows\n", Psych::BadAlias]
    ].each do |yaml, psych_error|
      error = assert_raises(Lockfile::ParseError) { Lockfile.parse(yaml) }
      assert_kind_of psych_error, error.cause
    end
  end

  def test_exact_version_policy_rejects_old_and_future_versions
    assert_raises(Lockfile::UnsupportedVersionError) do
      Lockfile.parse("version: v0.0.1\n", policy: Lockfile::VersionPolicy.exact("v0.0.2"))
    end
    assert_raises(Lockfile::FutureVersionError) do
      Lockfile.parse("version: v0.0.3\n", policy: Lockfile::VersionPolicy.exact("v0.0.2"))
    end
  end

  def test_rejects_pin_ref_mismatch_and_uses_cycles
    assert_raises(Lockfile::ParseError) { parse_with(VALID_ACTION.merge("ref" => "v3")) }

    yaml = <<~YAML
      version: v0.0.2
      dependencies:
        actions/a@v1:
          ref: v1
          commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
          owner_id: 1
          repo_id: 1
          uses: [actions/b@v1]
        actions/b@v1:
          ref: v1
          commit: sha1-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
          owner_id: 2
          repo_id: 2
          uses: [actions/a@v1]
    YAML
    assert_raises(Lockfile::ParseError) { Lockfile.parse(yaml) }
  end

  def test_scoped_validation_and_explicit_dependency_validation
    yaml = <<~YAML
      version: v0.0.2
      workflows:
        .github/workflows/ci.yml: [actions/good@v1]
      dependencies:
        actions/good@v1:
          ref: v1
          commit: sha1-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
          owner_id: 1
          repo_id: 1
        actions/bad@v1:
          ref: v1
          owner_id: 2
          repo_id: 2
          flavor: spicy
    YAML

    lockfile = Lockfile.parse(yaml, paths: [".github/workflows/ci.yml"])
    assert_equal "v1", lockfile.lookup_pin("actions", "good", "v1").ref
    assert_raises(Lockfile::ParseError) { lockfile.validate_dependency!("actions/bad@v1") }
    assert_raises(Lockfile::ParseError) { Lockfile.parse(yaml, paths: []) }
  end

  private

  def parse_with(action)
    body = action.map { |key, value| "    #{key}: #{value.inspect}\n" }.join
    Lockfile.parse("version: v0.0.2\ndependencies:\n  actions/checkout@v4:\n#{body}")
  end
end
