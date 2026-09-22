OPENAPI_URL := https://api.typesafe.ai/openapi.json

.PHONY: openapi-update generate

openapi-update:
	curl --fail --location --silent --show-error \
		$(OPENAPI_URL) \
		--output openapi.json

generate:
	$(MAKE) -C ../.. generate
