# frozen_string_literal: true

require "yaml"
require_relative "lockfile/version"

module GitHub
  module Actions
    module Lockfile
      PATH = ".github/workflows/actions.lock"
      SCHEMA_VERSION = "v0.0.2"
      MAX_PARSE_SIZE = 1 << 20
      SUPPORTED_VERSIONS = [SCHEMA_VERSION].freeze
      TOP_LEVEL_KEYS = %w[version workflows dependencies].freeze
      ACTION_KEYS = %w[ref commit owner_id repo_id uses].freeze
      REQUIRED_ACTION_KEYS = %w[ref commit owner_id repo_id].freeze
      DIGEST = /\A(?:sha1-[0-9a-f]{40}|sha256-[0-9a-f]{64})\z/i
      FULL_SHA = /\A(?:[0-9a-f]{40}|[0-9a-f]{64})\z/i

      class ParseError < StandardError
        attr_reader :line, :column

        def initialize(message, line: nil, column: nil)
          @line = line
          @column = column
          super(message)
        end
      end

      class UnsupportedVersionError < ParseError; end
      class FutureVersionError < ParseError; end

      VersionPolicy = Struct.new(:min, :max, keyword_init: true) do
        def self.exact(version)
          new(min: version, max: version)
        end
      end

      DEFAULT_POLICY = VersionPolicy.exact(SCHEMA_VERSION).freeze

      Action = Struct.new(:ref, :commit, :owner_id, :repo_id, :uses, keyword_init: true)

      class File
        attr_reader :version, :workflows, :dependencies

        def initialize(version:, workflows:, dependencies:, raw_dependencies:)
          @version = version
          @workflows = workflows.freeze
          @dependencies = dependencies.freeze
          @raw_dependencies = raw_dependencies.freeze
          freeze
        end

        def lookup_workflow(path)
          workflows[path]
        end

        def lookup_pin(owner, repo, ref)
          dependencies["#{owner.downcase}/#{repo.downcase}@#{ref}"]
        end

        def validate_dependency!(pin)
          action = dependencies.fetch(pin) do
            raise ParseError, "dependency #{pin.inspect} is not present"
          end
          Lockfile.validate_dependency!(pin, @raw_dependencies.fetch(pin))
          action
        end
      end

      module_function

      def parse(contents, policy: DEFAULT_POLICY, paths: nil)
        unless contents.is_a?(String)
          raise ArgumentError, "contents must be a String"
        end
        if contents.bytesize > MAX_PARSE_SIZE
          raise ParseError, "lockfile too large: #{contents.bytesize} bytes (max #{MAX_PARSE_SIZE})"
        end

        document = YAML.safe_load(
          contents,
          permitted_classes: [],
          permitted_symbols: [],
          aliases: false
        )
        parse_document(document, policy, paths)
      rescue Psych::SyntaxError, Psych::DisallowedClass, Psych::BadAlias => error
        raise ParseError.new(
          error.message,
          line: error.respond_to?(:line) ? error.line : nil,
          column: error.respond_to?(:column) ? error.column : nil
        ), cause: error
      end

      def parse_document(document, policy, paths)
        require_hash!(document, "lockfile")
        reject_unknown_keys!(document, TOP_LEVEL_KEYS, "lockfile")

        version = document["version"]
        raise ParseError, "dependency lockfile version is required" if version.nil? || version == ""
        raise ParseError, "lockfile version must be a string" unless version.is_a?(String)

        check_version!(version, policy)
        workflows = parse_workflows(document.fetch("workflows", {}))
        raw_dependencies = document.fetch("dependencies", {})
        require_hash!(raw_dependencies, "dependencies")

        selected_paths = Array(paths)
        in_scope = if selected_paths.empty?
                     nil
                   else
                     selected_paths.flat_map { |path| workflows.fetch(path, []) }.to_h { |pin| [pin, true] }
                   end
        normalized_raw = {}
        dependencies = raw_dependencies.each_with_object({}) do |(source_pin, raw_action), result|
          pin = canonical_pin!(source_pin, "dependency key")
          require_hash!(raw_action, "dependency #{source_pin.inspect}")
          validate_dependency!(source_pin, raw_action) if in_scope.nil? || in_scope[source_pin] || in_scope[pin]
          action = action_from(raw_action)

          if result.key?(pin) && result[pin] != action
            raise ParseError, "duplicate action key #{pin.inspect} after canonicalization with differing metadata"
          end
          result[pin] = action
          normalized_raw[pin] = raw_action
        end

        detect_uses_cycle!(dependencies)
        File.new(
          version: version,
          workflows: workflows,
          dependencies: dependencies,
          raw_dependencies: normalized_raw
        )
      end

      def parse_workflows(raw_workflows)
        require_hash!(raw_workflows, "workflows")
        raw_workflows.each_with_object({}) do |(path, pins), result|
          validate_workflow_path!(path)
          unless pins.is_a?(Array)
            raise ParseError, "workflow #{path.inspect} dependencies must be an array"
          end
          result[path] = pins.map { |pin| canonical_pin!(pin, "workflow #{path.inspect} dependency") }.freeze
        end
      end

      def validate_dependency!(pin, action)
        require_hash!(action, "dependency #{pin.inspect}")
        reject_unknown_keys!(action, ACTION_KEYS, "dependency #{pin.inspect}")
        missing = REQUIRED_ACTION_KEYS.find { |key| !action.key?(key) }
        raise ParseError, "missing required action field #{missing.inspect} for dependency #{pin.inspect}" if missing

        ref = action["ref"]
        unless ref.is_a?(String) && !ref.empty?
          raise ParseError, "action field \"ref\" must be a non-empty string for dependency #{pin.inspect}"
        end

        commit = action["commit"]
        unless commit.is_a?(String) && DIGEST.match?(commit)
          raise ParseError, "action field \"commit\" must be an algo-hex digest for dependency #{pin.inspect}"
        end

        %w[owner_id repo_id].each do |key|
          value = action[key]
          unless value.is_a?(Integer) && value.positive?
            raise ParseError, "action field #{key.inspect} must be a positive integer for dependency #{pin.inspect}"
          end
        end

        uses = action.fetch("uses", [])
        raise ParseError, "action field \"uses\" must be an array for dependency #{pin.inspect}" unless uses.is_a?(Array)
        uses.each { |entry| canonical_pin!(entry, "uses entry in dependency #{pin.inspect}") }

        canonical = canonical_pin!(pin, "dependency key")
        pin_ref = canonical.split("@", 2).last
        unless FULL_SHA.match?(pin_ref)
          if ref != pin_ref
            raise ParseError, "action body ref #{ref.inspect} does not match pin key ref #{pin_ref.inspect} for dependency #{pin.inspect}"
          end
        else
          commit_hex = commit.split("-", 2).last
          unless pin_ref.casecmp?(commit_hex)
            raise ParseError, "pin key ref is a full SHA but commit digest #{commit.inspect} does not match for dependency #{pin.inspect}"
          end
        end

        true
      end

      def action_from(raw)
        uses = raw["uses"]
        if !uses.nil? && !uses.is_a?(Array)
          raise ParseError, "action field \"uses\" must be an array"
        end

        Action.new(
          ref: raw["ref"],
          commit: raw["commit"],
          owner_id: raw["owner_id"],
          repo_id: raw["repo_id"],
          uses: Array(uses).map { |pin| canonical_pin!(pin, "uses entry") }.freeze
        ).freeze
      end

      def canonical_pin!(pin, context)
        unless pin.is_a?(String)
          raise ParseError, "#{context} must be a string"
        end

        at = pin.index("@")
        repo_path = at && pin[0...at]
        ref = at && pin[(at + 1)..]
        parts = repo_path&.split("/", -1)
        unless at&.positive? && !ref.empty? && !ref.include?(":") &&
               parts&.length == 2 && parts.none?(&:empty?)
          raise ParseError, "#{context} #{pin.inspect} is not a valid pin (expected OWNER/REPO@REF)"
        end

        "#{parts[0].downcase}/#{parts[1].downcase}@#{ref}"
      end

      def validate_workflow_path!(path)
        raise ParseError, "workflow path key must be a string" unless path.is_a?(String)
        raise ParseError, "workflow path key must not be empty" if path.empty?
        if path.start_with?("/") || path.include?("\\") || path.include?(":") ||
           path.each_codepoint.any? { |codepoint| codepoint <= 0x1f || codepoint == 0x7f } ||
           path.split("/").include?("..")
          raise ParseError, "unsafe workflow path key: #{path.inspect}"
        end
      end

      def check_version!(version, policy)
        unless policy.is_a?(VersionPolicy) && policy.min && policy.max
          raise ArgumentError, "policy must be a VersionPolicy with min and max"
        end

        actual = version_parts(version)
        minimum = version_parts(policy.min)
        maximum = version_parts(policy.max)
        unless actual && minimum && maximum
          raise UnsupportedVersionError, "unsupported dependency lockfile version #{version.inspect}"
        end

        if (actual <=> maximum).positive?
          raise FutureVersionError, "lockfile version #{version} is newer than this consumer supports (maximum #{policy.max})"
        end
        if (actual <=> minimum).negative? || !SUPPORTED_VERSIONS.include?(version)
          raise UnsupportedVersionError, "lockfile version #{version} is older than this consumer supports (minimum #{policy.min})"
        end
      end

      def version_parts(version)
        match = /\Av?(\d+)\.(\d+)\.(\d+)\z/.match(version)
        match && match.captures.map(&:to_i)
      end

      def detect_uses_cycle!(dependencies)
        state = {}
        visit = lambda do |pin|
          raise ParseError, "uses cycle detected at dependency #{pin.inspect}" if state[pin] == :visiting
          return if state[pin] == :visited

          state[pin] = :visiting
          dependencies[pin]&.uses&.each { |child| visit.call(child) }
          state[pin] = :visited
        end
        dependencies.each_key { |pin| visit.call(pin) }
      end

      def reject_unknown_keys!(hash, allowed, context)
        unknown = hash.keys.find { |key| !key.is_a?(String) || !allowed.include?(key) }
        raise ParseError, "unknown #{context} field #{unknown.inspect}" if unknown
      end

      def require_hash!(value, context)
        raise ParseError, "#{context} must be a mapping" unless value.is_a?(Hash)
      end

      private_class_method :parse_document, :parse_workflows, :action_from,
                           :canonical_pin!, :validate_workflow_path!, :check_version!,
                           :version_parts, :detect_uses_cycle!, :reject_unknown_keys!,
                           :require_hash!
    end
  end
end
