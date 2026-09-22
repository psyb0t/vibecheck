# Project Makefile
# Add your custom targets here - they will override servicepack defaults

# Override framework variables (optional)
# MIN_TEST_COVERAGE := 95

# Include servicepack framework commands
include Makefile.servicepack

# Custom targets below this line
# Note: Override warnings are expected and can be ignored

# Example: override a framework command by uncommenting and editing this.
#
# Left COMMENTED on purpose. As a live target it shadowed the framework's real
# `build`, so `make build` printed a line and produced no binary. That broke
# the README's own Quick Start (`make own` → `make build` → ./build/<name> run)
# for everyone who followed it.
#
# build: ## Custom build command
# 	@echo "Running custom build..."

# Add your custom targets below this line

COMPOSE_FILE ?= docker-compose.yml
COMPOSE_ENV_FILE ?= .env.example
COMPOSE_RENDERED := build/compose-rendered.json

# Gitignored credentials for the paid provider. Only test-real reads it.
REAL_PROVIDER_ENV_FILE ?= .env.real

.PHONY: audit-compose test-api test-real

# The framework's `test` and `test-integration` do not pass -tags=integration,
# so the testcontainers suites they would run are compiled out; only
# `test-coverage` sets the tag. This target runs them on their own, which is
# what you want while iterating on the API surface.
test-api: dev-image ## Run the production-image API suite (testcontainers)
	@$(DEV_RUN_DIND) go test -race -count=1 -timeout=1800s \
		-tags=integration ./tests/...

# Opt-in only: it calls the real TypeSafe API and bills a real account, so it
# is never part of `make test` and never runs in CI. Put the credential in
# the gitignored $(REAL_PROVIDER_ENV_FILE); without it the target refuses.
#
# The credential reaches the container through the framework's documented
# DEV_RUN_DIND_EXTRA_ARGS hook, scoped to this target alone, so no ordinary
# target can pull a real key into its environment.
test-real: DEV_RUN_DIND_EXTRA_ARGS = --env-file "$(REAL_PROVIDER_ENV_FILE)"
test-real: dev-image ## Run the opt-in suite against the live Jev provider
	@test -f "$(REAL_PROVIDER_ENV_FILE)" || { \
		echo "missing $(REAL_PROVIDER_ENV_FILE), see docs/testing.md"; \
		exit 1; \
	}
	@$(DEV_RUN_DIND) go test -race -count=1 -timeout=600s \
		-tags='integration realprovider' ./tests/realprovider/...

# Renders the compose file on the host, because `docker compose` is a host
# tool, then runs the assertions in the development image like every other
# check. What gets audited is the rendered document, so anchors, merges,
# profiles, and variable substitution are already resolved by the time the
# policy floor sees it.
audit-compose: dev-image ## Audit the production compose file against the hardened floor
	@mkdir -p "$(dir $(COMPOSE_RENDERED))"
	@docker compose --env-file "$(COMPOSE_ENV_FILE)" -f "$(COMPOSE_FILE)" \
		config --format json >"$(COMPOSE_RENDERED)"
	@$(DEV_RUN) bash scripts/audit-compose-floor.sh "$(COMPOSE_RENDERED)"
	@rm -f "$(COMPOSE_RENDERED)"
