UPSTREAM_REPO := "buttplugio/buttplug"
UPSTREAM_REVISION := "3334d6e3a7e827072aeaca5cdb3dd4dd43f854a7"
UPSTREAM_SCHEMA_PATH := "/crates/buttplug_core/schema/buttplug-schema.json" 

TARGET_SCHEMA := "./buttplug-schema.json"

# update the schema from upstream
update: fetch patch generate
	@echo "Updated with upstream revision {{UPSTREAM_REVISION}}."

# generate go code from the schema
generate *args:
	@go run ../cmd/buttplug-generate {{args}}

# fetch the buttplug schema from upstream repo
[private]
@fetch:
	curl -L -o {{TARGET_SCHEMA}} \
		https://raw.githubusercontent.com/{{UPSTREAM_REPO}}/{{UPSTREAM_REVISION}}{{UPSTREAM_SCHEMA_PATH}}

# patch the fetched schema if needed
[private]
@patch:
	# patch the Data array in SensorReadingV3 to be int instead of bytes per
	# Rust schema which uses int32.
	jq '\
		del(.messages.SpecV3Messages.SensorReading.properties.Data.items.maximum)\
	' {{TARGET_SCHEMA}} | sponge {{TARGET_SCHEMA}}
