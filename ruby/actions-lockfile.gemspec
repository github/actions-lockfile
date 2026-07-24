# frozen_string_literal: true

require_relative "lib/actions/lockfile/version"

Gem::Specification.new do |spec|
  spec.name = "actions-lockfile"
  spec.version = GitHub::Actions::Lockfile::VERSION
  spec.authors = ["GitHub"]
  spec.summary = "Parse GitHub Actions dependency lockfiles"
  spec.homepage = "https://github.com/github/actions-lockfile"
  spec.license = "MIT"
  spec.required_ruby_version = ">= 3.1"
  spec.files = Dir["lib/**/*.rb"]
  spec.require_paths = ["lib"]

  spec.metadata = {
    "source_code_uri" => spec.homepage,
    "rubygems_mfa_required" => "true"
  }

  spec.add_development_dependency "minitest", "~> 5.0"
  spec.add_development_dependency "rake", "~> 13.0"
end
