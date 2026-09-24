package server

import (
	"context"
	"fmt"

	"tokenhub/backend/internal/dbschema"
)

func gatewaySchemaMigrationSQLite(ctx context.Context, db dbschema.MigrationExecer) error {
	for _, statement := range []string{
		"CREATE TABLE IF NOT EXISTS `gateway_tenants` (`id` text,`external_tenant_id` text,`name` text,`status` text,`version` integer,`synced_at` datetime,`deleted_at` datetime,`created_at` datetime,`updated_at` datetime,PRIMARY KEY (`id`))",
		"CREATE TABLE IF NOT EXISTS `gateway_organizations` (`id` text,`tenant_id` text,`external_organization_id` text,`parent_id` text,`name` text,`status` text,`version` integer,`synced_at` datetime,`deleted_at` datetime,`created_at` datetime,`updated_at` datetime,PRIMARY KEY (`id`))",
		"CREATE TABLE IF NOT EXISTS `gateway_principals` (`id` text,`tenant_id` text,`external_principal_id` text,`external_membership_id` text,`display_name` text,`status` text,`version` integer,`source_occurred_at` datetime,`synced_at` datetime,`deleted_at` datetime,`created_at` datetime,`updated_at` datetime,PRIMARY KEY (`id`))",
		"CREATE TABLE IF NOT EXISTS `gateway_principal_organization_bindings` (`id` text,`tenant_id` text,`external_membership_id` text,`principal_id` text,`organization_id` text,`status` text,`version` integer,`synced_at` datetime,`deleted_at` datetime,`created_at` datetime,`updated_at` datetime,PRIMARY KEY (`id`))",
		"CREATE TABLE IF NOT EXISTS `gateway_projects` (`id` text,`tenant_id` text,`external_project_id` text,`organization_id` text,`owner_principal_id` text,`name` text,`status` text,`version` integer,`synced_at` datetime,`deleted_at` datetime,`created_at` datetime,`updated_at` datetime,PRIMARY KEY (`id`))",
		"CREATE TABLE IF NOT EXISTS `gateway_workloads` (`id` text,`tenant_id` text,`external_workload_id` text,`project_id` text,`owner_principal_id` text,`name` text,`workload_type` text,`environment` text,`status` text,`version` integer,`synced_at` datetime,`deleted_at` datetime,`created_at` datetime,`updated_at` datetime,PRIMARY KEY (`id`))",
		"CREATE TABLE IF NOT EXISTS `integration_inbox` (`event_id` text,`event_digest` text,`tenant_id` text,`event_type` text,`aggregate_type` text,`aggregate_id` text,`aggregate_version` integer,`applied_version` integer,`status` text,`entity_type` text,`token_hub_entity_id` text,`received_at` datetime,`processed_at` datetime,`created_at` datetime,`updated_at` datetime,PRIMARY KEY (`event_id`))",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	for _, column := range []struct {
		name     string
		typeName string
	}{
		{"tenant_external_id", "text"}, {"project_external_id", "text"},
		{"principal_type", "text"}, {"principal_external_id", "text"},
		{"environment", "text"}, {"managed_by", "text"},
		{"control_request_id", "text"}, {"control_request_digest", "text"},
		{"key_ciphertext", "text"}, {"rate_limit_rpm", "integer"},
		{"token_limit_tpm", "integer"},
	} {
		exists, err := sqliteColumnExists(ctx, db, "api_keys", column.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE `api_keys` ADD COLUMN `%s` %s", column.name, column.typeName)); err != nil {
				return err
			}
		}
	}
	for _, statement := range []string{
		"CREATE INDEX IF NOT EXISTS `idx_api_key_gateway_scope` ON `api_keys`(`tenant_external_id`,`project_external_id`)",
		"CREATE UNIQUE INDEX IF NOT EXISTS `idx_api_keys_control_request_id` ON `api_keys`(`control_request_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_api_keys_environment` ON `api_keys`(`environment`)",
		"CREATE INDEX IF NOT EXISTS `idx_api_keys_managed_by` ON `api_keys`(`managed_by`)",
		"CREATE INDEX IF NOT EXISTS `idx_api_keys_principal_external_id` ON `api_keys`(`principal_external_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_api_keys_principal_type` ON `api_keys`(`principal_type`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_tenants_status` ON `gateway_tenants`(`status`)",
		"CREATE UNIQUE INDEX IF NOT EXISTS `idx_gateway_tenants_external_tenant_id` ON `gateway_tenants`(`external_tenant_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_organizations_status` ON `gateway_organizations`(`status`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_organizations_parent_id` ON `gateway_organizations`(`parent_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_organizations_tenant_id` ON `gateway_organizations`(`tenant_id`)",
		"CREATE UNIQUE INDEX IF NOT EXISTS `idx_gateway_org_external` ON `gateway_organizations`(`tenant_id`,`external_organization_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_principals_status` ON `gateway_principals`(`status`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_principals_external_membership_id` ON `gateway_principals`(`external_membership_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_principals_tenant_id` ON `gateway_principals`(`tenant_id`)",
		"CREATE UNIQUE INDEX IF NOT EXISTS `idx_gateway_principal_external` ON `gateway_principals`(`tenant_id`,`external_principal_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_principal_organization_bindings_status` ON `gateway_principal_organization_bindings`(`status`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_principal_organization_bindings_organization_id` ON `gateway_principal_organization_bindings`(`organization_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_principal_organization_bindings_principal_id` ON `gateway_principal_organization_bindings`(`principal_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_principal_organization_bindings_tenant_id` ON `gateway_principal_organization_bindings`(`tenant_id`)",
		"CREATE UNIQUE INDEX IF NOT EXISTS `idx_gateway_org_binding_external` ON `gateway_principal_organization_bindings`(`tenant_id`,`external_membership_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_projects_status` ON `gateway_projects`(`status`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_projects_owner_principal_id` ON `gateway_projects`(`owner_principal_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_projects_organization_id` ON `gateway_projects`(`organization_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_projects_tenant_id` ON `gateway_projects`(`tenant_id`)",
		"CREATE UNIQUE INDEX IF NOT EXISTS `idx_gateway_project_external` ON `gateway_projects`(`tenant_id`,`external_project_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_workloads_status` ON `gateway_workloads`(`status`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_workloads_environment` ON `gateway_workloads`(`environment`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_workloads_owner_principal_id` ON `gateway_workloads`(`owner_principal_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_workloads_project_id` ON `gateway_workloads`(`project_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_gateway_workloads_tenant_id` ON `gateway_workloads`(`tenant_id`)",
		"CREATE UNIQUE INDEX IF NOT EXISTS `idx_gateway_workload_external` ON `gateway_workloads`(`tenant_id`,`external_workload_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_integration_inbox_status` ON `integration_inbox`(`status`)",
		"CREATE INDEX IF NOT EXISTS `idx_integration_inbox_aggregate` ON `integration_inbox`(`aggregate_type`,`aggregate_id`,`aggregate_version`)",
		"CREATE INDEX IF NOT EXISTS `idx_integration_inbox_tenant_id` ON `integration_inbox`(`tenant_id`)",
		"CREATE INDEX IF NOT EXISTS `idx_usage_records_project_created` ON `usage_records`(`project_id`,`created_at`)",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

var gatewaySchemaMigrationPostgresStatements = []string{
	`ALTER TABLE "api_keys"
		ADD COLUMN IF NOT EXISTS "tenant_external_id" text,
		ADD COLUMN IF NOT EXISTS "project_external_id" text,
		ADD COLUMN IF NOT EXISTS "principal_type" text,
		ADD COLUMN IF NOT EXISTS "principal_external_id" text,
		ADD COLUMN IF NOT EXISTS "environment" text,
		ADD COLUMN IF NOT EXISTS "managed_by" text,
		ADD COLUMN IF NOT EXISTS "control_request_id" text,
		ADD COLUMN IF NOT EXISTS "control_request_digest" text,
		ADD COLUMN IF NOT EXISTS "key_ciphertext" text,
		ADD COLUMN IF NOT EXISTS "rate_limit_rpm" bigint,
		ADD COLUMN IF NOT EXISTS "token_limit_tpm" bigint`,
	`CREATE TABLE IF NOT EXISTS "gateway_tenants" ("id" text,"external_tenant_id" text,"name" text,"status" text,"version" bigint,"synced_at" timestamptz,"deleted_at" timestamptz,"created_at" timestamptz,"updated_at" timestamptz,PRIMARY KEY ("id"))`,
	`CREATE TABLE IF NOT EXISTS "gateway_organizations" ("id" text,"tenant_id" text,"external_organization_id" text,"parent_id" text,"name" text,"status" text,"version" bigint,"synced_at" timestamptz,"deleted_at" timestamptz,"created_at" timestamptz,"updated_at" timestamptz,PRIMARY KEY ("id"))`,
	`CREATE TABLE IF NOT EXISTS "gateway_principals" ("id" text,"tenant_id" text,"external_principal_id" text,"external_membership_id" text,"display_name" text,"status" text,"version" bigint,"source_occurred_at" timestamptz,"synced_at" timestamptz,"deleted_at" timestamptz,"created_at" timestamptz,"updated_at" timestamptz,PRIMARY KEY ("id"))`,
	`CREATE TABLE IF NOT EXISTS "gateway_principal_organization_bindings" ("id" text,"tenant_id" text,"external_membership_id" text,"principal_id" text,"organization_id" text,"status" text,"version" bigint,"synced_at" timestamptz,"deleted_at" timestamptz,"created_at" timestamptz,"updated_at" timestamptz,PRIMARY KEY ("id"))`,
	`CREATE TABLE IF NOT EXISTS "gateway_projects" ("id" text,"tenant_id" text,"external_project_id" text,"organization_id" text,"owner_principal_id" text,"name" text,"status" text,"version" bigint,"synced_at" timestamptz,"deleted_at" timestamptz,"created_at" timestamptz,"updated_at" timestamptz,PRIMARY KEY ("id"))`,
	`CREATE TABLE IF NOT EXISTS "gateway_workloads" ("id" text,"tenant_id" text,"external_workload_id" text,"project_id" text,"owner_principal_id" text,"name" text,"workload_type" text,"environment" text,"status" text,"version" bigint,"synced_at" timestamptz,"deleted_at" timestamptz,"created_at" timestamptz,"updated_at" timestamptz,PRIMARY KEY ("id"))`,
	`CREATE TABLE IF NOT EXISTS "integration_inbox" ("event_id" text,"event_digest" text,"tenant_id" text,"event_type" text,"aggregate_type" text,"aggregate_id" text,"aggregate_version" bigint,"applied_version" bigint,"status" text,"entity_type" text,"token_hub_entity_id" text,"received_at" timestamptz,"processed_at" timestamptz,"created_at" timestamptz,"updated_at" timestamptz,PRIMARY KEY ("event_id"))`,
	`CREATE UNIQUE INDEX IF NOT EXISTS "idx_api_keys_control_request_id" ON "api_keys" ("control_request_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_api_keys_environment" ON "api_keys" ("environment")`,
	`CREATE INDEX IF NOT EXISTS "idx_api_keys_managed_by" ON "api_keys" ("managed_by")`,
	`CREATE INDEX IF NOT EXISTS "idx_api_keys_principal_external_id" ON "api_keys" ("principal_external_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_api_keys_principal_type" ON "api_keys" ("principal_type")`,
	`CREATE INDEX IF NOT EXISTS "idx_api_key_gateway_scope" ON "api_keys" ("tenant_external_id","project_external_id")`,
	`CREATE UNIQUE INDEX IF NOT EXISTS "idx_gateway_tenants_external_tenant_id" ON "gateway_tenants" ("external_tenant_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_tenants_status" ON "gateway_tenants" ("status")`,
	`CREATE UNIQUE INDEX IF NOT EXISTS "idx_gateway_org_external" ON "gateway_organizations" ("tenant_id","external_organization_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_organizations_status" ON "gateway_organizations" ("status")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_organizations_parent_id" ON "gateway_organizations" ("parent_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_organizations_tenant_id" ON "gateway_organizations" ("tenant_id")`,
	`CREATE UNIQUE INDEX IF NOT EXISTS "idx_gateway_principal_external" ON "gateway_principals" ("tenant_id","external_principal_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_principals_status" ON "gateway_principals" ("status")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_principals_external_membership_id" ON "gateway_principals" ("external_membership_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_principals_tenant_id" ON "gateway_principals" ("tenant_id")`,
	`CREATE UNIQUE INDEX IF NOT EXISTS "idx_gateway_org_binding_external" ON "gateway_principal_organization_bindings" ("tenant_id","external_membership_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_principal_organization_bindings_status" ON "gateway_principal_organization_bindings" ("status")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_principal_organization_bindings_organization_id" ON "gateway_principal_organization_bindings" ("organization_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_principal_organization_bindings_principal_id" ON "gateway_principal_organization_bindings" ("principal_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_principal_organization_bindings_tenant_id" ON "gateway_principal_organization_bindings" ("tenant_id")`,
	`CREATE UNIQUE INDEX IF NOT EXISTS "idx_gateway_project_external" ON "gateway_projects" ("tenant_id","external_project_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_projects_status" ON "gateway_projects" ("status")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_projects_owner_principal_id" ON "gateway_projects" ("owner_principal_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_projects_organization_id" ON "gateway_projects" ("organization_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_projects_tenant_id" ON "gateway_projects" ("tenant_id")`,
	`CREATE UNIQUE INDEX IF NOT EXISTS "idx_gateway_workload_external" ON "gateway_workloads" ("tenant_id","external_workload_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_workloads_status" ON "gateway_workloads" ("status")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_workloads_environment" ON "gateway_workloads" ("environment")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_workloads_owner_principal_id" ON "gateway_workloads" ("owner_principal_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_workloads_project_id" ON "gateway_workloads" ("project_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_gateway_workloads_tenant_id" ON "gateway_workloads" ("tenant_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_integration_inbox_status" ON "integration_inbox" ("status")`,
	`CREATE INDEX IF NOT EXISTS "idx_integration_inbox_aggregate" ON "integration_inbox" ("aggregate_type","aggregate_id","aggregate_version")`,
	`CREATE INDEX IF NOT EXISTS "idx_integration_inbox_tenant_id" ON "integration_inbox" ("tenant_id")`,
	`CREATE INDEX IF NOT EXISTS "idx_usage_records_project_created" ON "usage_records" ("project_id","created_at")`,
}

func gatewaySchemaMigrationPostgres(ctx context.Context, db dbschema.MigrationExecer) error {
	for _, statement := range gatewaySchemaMigrationPostgresStatements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
