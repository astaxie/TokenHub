package persistence

import (
	"strings"

	"gorm.io/gorm"
)

const attributionVersion = "tokenhub_attribution_version"
const attributionProvider = "tokenhub_provider_id"
const attributionResource = "tokenhub_resource_id"

func snapshotAttribution(metadata, config map[string]string, account string) map[string]string {
	result := cloneStringMap(metadata)
	if result == nil {
		result = map[string]string{}
	}
	result[attributionVersion] = "1"
	result[attributionProvider] = firstAttributionValue(metadata["provider_id"], config["provider_id"])
	result[attributionResource] = firstAttributionValue(metadata["provider_resource_id"], metadata["resource_id"], config["provider_resource_id"], account)
	return result
}

func firstAttributionValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// BackfillRecordAttribution upgrades legacy rows before connector changes and
// statement reads. Once marked, even an empty identity is an immutable snapshot.
func BackfillRecordAttribution(db *gorm.DB, connectorID string) error {
	statement := AttributionBackfillSQL(db.Dialector.Name())
	if connectorID != "" {
		statement += " AND connector_id = ?"
		return db.Exec(statement, connectorID).Error
	}
	return db.Exec(statement).Error
}

func AttributionBackfillSQL(dialect string) string {
	extract := func(column, key string) string {
		if dialect == "postgres" {
			return "(COALESCE(NULLIF(" + column + ", ''), '{}')::jsonb ->> '" + key + "')"
		}
		return "json_extract(COALESCE(NULLIF(" + column + ", ''), '{}'), '$." + key + "')"
	}
	provider := "COALESCE(NULLIF(" + extract("metadata", "provider_id") + ", ''), (SELECT " + extract("config", "provider_id") + " FROM billing_connectors WHERE id = billing_records.connector_id), '')"
	resource := "COALESCE(NULLIF(" + extract("metadata", "provider_resource_id") + ", ''), NULLIF(" + extract("metadata", "resource_id") + ", ''), (SELECT NULLIF(" + extract("config", "provider_resource_id") + ", '') FROM billing_connectors WHERE id = billing_records.connector_id), account_id, '')"
	var value string
	if dialect == "postgres" {
		value = "(COALESCE(NULLIF(metadata, ''), '{}')::jsonb || jsonb_build_object('tokenhub_attribution_version', '1', 'tokenhub_provider_id', " + provider + ", 'tokenhub_resource_id', " + resource + "))::text"
	} else {
		value = "json_set(COALESCE(NULLIF(metadata, ''), '{}'), '$.tokenhub_attribution_version', '1', '$.tokenhub_provider_id', " + provider + ", '$.tokenhub_resource_id', " + resource + ")"
	}
	return "UPDATE billing_records SET metadata = " + value + " WHERE COALESCE(" + extract("metadata", attributionVersion) + ", '') != '1'"
}
