BIN := gh-actions-lockfile
RUNNER_ROOT ?= $(HOME)/ghq/github.com/actions/runner
RUNNER_BIN := $(RUNNER_ROOT)/_layout/bin
RUNNER_DIAG := $(RUNNER_ROOT)/_layout/_diag

.PHONY: build test test-unit lint clean install help
.PHONY: serve run-runner run-runner-lockfile run-runner-tampered build-runner runner-logs demo

## Build the binary
build:
	go build -o $(BIN) ./cmd/gh-actions-lockfile

## Run all tests
test:
	go test ./... -count=1

## Run unit tests only (no network)
test-unit:
	go test ./pkg/lockfile/... ./pkg/actionmeta/... -count=1

## Lint
lint:
	go vet ./...

## Install to GOPATH/bin
install:
	go install ./cmd/gh-actions-lockfile

## Clean build artifacts
clean:
	rm -f $(BIN)

## Pin a workflow file
pin: build
	@test -n "$(FILE)" || (echo "usage: make pin FILE=path/to/workflow.yml" && exit 1)
	./$(BIN) pin $(FILE)

## Validate a workflow file
validate: build
	@test -n "$(FILE)" || (echo "usage: make validate FILE=path/to/workflow.yml" && exit 1)
	./$(BIN) validate $(FILE)

## Pin all real-world corpus workflows (dry-run)
corpus: build
	@for f in testdata/real-world/*.yml; do \
		name=$$(basename "$$f"); \
		printf "\033[1m%s\033[0m\n" "$$name"; \
		./$(BIN) pin --dry-run "$$f" 2>&1 | sed 's/^/  /'; \
		echo ""; \
	done

# ---------------------------------------------------------------------------
# Dev: runner integration (fake launch + harness)
# ---------------------------------------------------------------------------

# Helper: run the harness, capture the runner log, print enforcement output.
# Args passed via env: _LOCKFILE_DEPS, _USES
define run_harness
	@GITHUB_TOKEN=$$(gh auth token) LAUNCH_ENDPOINT=http://localhost:9399 \
		LOCKFILE_DEPS='$(_LOCKFILE_DEPS)' \
		dotnet run --project dev/harness-dotnet -- \
		"$(RUNNER_BIN)" \
		"$(_USES)" 2>&1; \
	rc=$$?; \
	LOGFILE=$$(ls -t $(RUNNER_DIAG)/Worker_*.log 2>/dev/null | head -1); \
	echo ""; \
	if [ -n "$$LOGFILE" ]; then \
		if grep -q "LOCKFILE VIOLATION" "$$LOGFILE"; then \
			grep "LOCKFILE VIOLATION" "$$LOGFILE" | head -1 | sed 's/.*LOCKFILE/  \x1b[31mLOCKFILE/' | sed 's/$$/\x1b[0m/'; \
		elif grep -q "verified" "$$LOGFILE"; then \
			grep "verified" "$$LOGFILE" | sed 's/.*Enqueue web console line queue: /  \x1b[32m/' | sed 's/$$/\x1b[0m/'; \
		fi; \
		download=$$(grep "Download action" "$$LOGFILE" | head -1); \
		if [ -n "$$download" ]; then \
			echo "$$download" | sed 's/.*Enqueue web console line queue: /  \x1b[36m/' | sed 's/$$/\x1b[0m/'; \
		fi; \
	fi; \
	echo ""; \
	if [ $$rc -eq 100 ] || [ $$rc -eq 0 ]; then \
		printf '  \033[32m✓ runner exit %d (job completed)\033[0m\n' $$rc; \
	elif [ $$rc -eq 102 ]; then \
		if [ -n "$$LOGFILE" ] && grep -q "LOCKFILE VIOLATION" "$$LOGFILE"; then \
			printf '  \033[31m✗ runner exit 102 (lockfile violation)\033[0m\n'; \
		else \
			printf '  \033[33m✗ runner exit 102 (step execution failed -- not a lockfile issue)\033[0m\n'; \
		fi; \
	else \
		printf '  \033[33m? runner exit %d\033[0m\n' $$rc; \
	fi
endef

## Start fake Launch server (default port 9399)
serve: build
	./$(BIN) serve --port 9399

## Run real runner against fake Launch (usage: make run-runner USES="owner/repo@ref")
run-runner: build
	@test -n "$(USES)" || (echo "usage: make run-runner USES='nodeselector/actions-test-fixtures/simple-node@main'" && exit 1)
	$(eval _USES := $(USES))
	$(eval _LOCKFILE_DEPS := )
	$(run_harness)

## Run runner with correct lockfile deps (should pass)
run-runner-lockfile: build
	@printf '\033[1mCorrect lockfile deps:\033[0m\n'
	$(eval _USES := nodeselector/actions-test-fixtures/simple-node@main)
	$(eval _LOCKFILE_DEPS := ["github.com/nodeselector/actions-test-fixtures@main:sha1-ea53476fdc172d8552df5af9658a45a367e4f41d"])
	$(run_harness)

## Run runner with tampered lockfile deps (should fail)
run-runner-tampered: build
	@printf '\033[1mTampered lockfile deps:\033[0m\n'
	$(eval _USES := nodeselector/actions-test-fixtures/simple-node@main)
	$(eval _LOCKFILE_DEPS := ["github.com/nodeselector/actions-test-fixtures@main:sha1-deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"])
	$(run_harness)

## Run composite action with correct transitive deps (should pass both rounds)
run-runner-composite: build
	@printf '\033[1mComposite action -- correct transitive deps:\033[0m\n'
	$(eval _USES := nodeselector/actions-test-fixtures/simple-composite@main)
	$(eval _LOCKFILE_DEPS := ["github.com/nodeselector/actions-test-fixtures@main:sha1-ea53476fdc172d8552df5af9658a45a367e4f41d","github.com/nodeselector/actions-test-fixtures-b@main:sha1-92b7b0058bc223c6e9dd4e19ef9247c934ba7637"])
	$(run_harness)

## Run composite action with tampered transitive dep (should fail on second round)
run-runner-composite-tampered: build
	@printf '\033[1mComposite action -- tampered transitive dep (fixtures-b):    \033[0m\n'
	$(eval _USES := nodeselector/actions-test-fixtures/simple-composite@main)
	$(eval _LOCKFILE_DEPS := ["github.com/nodeselector/actions-test-fixtures@main:sha1-ea53476fdc172d8552df5af9658a45a367e4f41d","github.com/nodeselector/actions-test-fixtures-b@main:sha1-deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"])
	$(run_harness)

## Full enforcement demo: serve must be running, runs all five cases
demo: build
	@printf '\n\033[1;4m=== Test 1: Correct lockfile ===\033[0m\n'
	@$(MAKE) --no-print-directory run-runner-lockfile
	@printf '\n\033[1;4m=== Test 2: Tampered lockfile ===\033[0m\n'
	@$(MAKE) --no-print-directory run-runner-tampered
	@printf '\n\033[1;4m=== Test 3: No lockfile (backward compat) ===\033[0m\n'
	@$(MAKE) --no-print-directory run-runner USES="nodeselector/actions-test-fixtures/simple-node@main"
	@printf '\n\033[1;4m=== Test 4: Composite -- correct transitive deps ===\033[0m\n'
	@$(MAKE) --no-print-directory run-runner-composite
	@printf '\n\033[1;4m=== Test 5: Composite -- tampered transitive dep ===\033[0m\n'
	@$(MAKE) --no-print-directory run-runner-composite-tampered

## Build the runner from source (at RUNNER_ROOT)
build-runner:
	cd $(RUNNER_ROOT)/src && ./dev.sh build 2>&1 | tail -3
	@if [ ! -f $(RUNNER_ROOT)/_layout/externals/node20/bin/node ]; then \
		cd $(RUNNER_ROOT)/src/Misc && bash externals.sh "osx-arm64" 2>&1 | tail -1; \
	fi
	@echo '{"agentId":1,"agentName":"lockfile-test","poolId":1,"poolName":"Default","serverUrl":"http://localhost:9399","workFolder":"/tmp/actions-work","ephemeral":true}' > $(RUNNER_ROOT)/_layout/.runner
	@echo '{"scheme":"OAuth","data":{"clientId":"fake","authorizationUrl":"http://localhost:9399"}}' > $(RUNNER_ROOT)/_layout/.credentials
	@mkdir -p $(RUNNER_DIAG)
	@echo "Runner built: $(RUNNER_BIN)/Runner.Worker"

## Tail the latest runner worker log
runner-logs:
	@LOGFILE=$$(ls -t $(RUNNER_DIAG)/Worker_*.log 2>/dev/null | head -1); \
	if [ -z "$$LOGFILE" ]; then \
		echo "No runner logs found. Run a runner target first."; \
		exit 1; \
	fi; \
	echo "Tailing: $$LOGFILE"; \
	tail -f "$$LOGFILE" | grep --line-buffered -iE 'lockfile|violation|action|resolve|download|composite|error|warn'

## Show help
help:
	@echo "gh-actions-lockfile targets:"
	@echo ""
	@echo "  CLI:"
	@echo "    make build        Build the binary"
	@echo "    make test         Run all tests"
	@echo "    make pin FILE=x   Pin a workflow"
	@echo "    make validate FILE=x"
	@echo "    make corpus       Dry-run pin against real-world workflows"
	@echo ""
	@echo "  Runner integration (start 'make serve' first):"
	@echo "    make serve              Start fake Launch on :9399"
	@echo "    make demo               Run all three enforcement cases"
	@echo "    make run-runner-lockfile Correct lockfile (should pass)"
	@echo "    make run-runner-tampered Tampered lockfile (should fail)"
	@echo "    make run-runner USES=x  Run with custom action ref"
	@echo "    make build-runner       Build runner from source"
	@echo "    make runner-logs        Tail latest runner log"
	@echo ""
	@echo "  RUNNER_ROOT=$(RUNNER_ROOT)"
