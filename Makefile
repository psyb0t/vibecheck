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

.PHONY: audit-compose

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
