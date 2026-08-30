package plugin

import "testing"

func TestManifestPermissionDescriptorsNormalizeDataAndNetworkPermissions(t *testing.T) {
	descriptors := ManifestPermissionDescriptors(ManifestPermissions{
		Network: ManifestNetworkPermissions{Allow: []string{"https://api.example.com", "https://api.example.com", "*.example.net"}},
		Data: ManifestDataPermissions{
			Read:  []GatewayDataClass{DataProviderCredentials, DataAudit, DataAudit},
			Write: []GatewayDataClass{DataRequestBody},
		},
	})

	want := []PermissionDescriptor{
		{Kind: PermissionKindData, Name: "audit", Access: PermissionAccessRead, Sensitivity: PermissionSensitivityInternal},
		{Kind: PermissionKindData, Name: "provider_credentials", Access: PermissionAccessRead, Sensitivity: PermissionSensitivitySecret},
		{Kind: PermissionKindData, Name: "request_body", Access: PermissionAccessWrite, Sensitivity: PermissionSensitivitySensitive},
		{Kind: PermissionKindNetwork, Name: "*.example.net", Access: PermissionAccessConnect, Sensitivity: PermissionSensitivityInternal},
		{Kind: PermissionKindNetwork, Name: "https://api.example.com", Access: PermissionAccessConnect, Sensitivity: PermissionSensitivityInternal},
	}
	if len(descriptors) != len(want) {
		t.Fatalf("descriptors = %+v, want %d entries", descriptors, len(want))
	}
	for index, expected := range want {
		if descriptors[index] != expected {
			t.Fatalf("descriptor[%d] = %+v, want %+v", index, descriptors[index], expected)
		}
	}
}

func TestValidateManifestPermissionsRejectsUnsupportedDataClass(t *testing.T) {
	err := ValidateManifestPermissions(ManifestPermissions{
		Data: ManifestDataPermissions{Read: []GatewayDataClass{"raw_database"}},
	})
	if err == nil {
		t.Fatal("unsupported data permission was accepted")
	}
}

func TestValidateManifestPermissionsRejectsUnsafeNetworkTarget(t *testing.T) {
	err := ValidateManifestPermissions(ManifestPermissions{
		Network: ManifestNetworkPermissions{Allow: []string{"../etc/passwd"}},
	})
	if err == nil {
		t.Fatal("unsafe network permission target was accepted")
	}
}

func TestSupportedPermissionCatalogIsStable(t *testing.T) {
	if len(SupportedPermissionKinds()) != 2 {
		t.Fatalf("permission kinds = %+v", SupportedPermissionKinds())
	}
	if len(SupportedPermissionSensitivities()) != 4 {
		t.Fatalf("permission sensitivities = %+v", SupportedPermissionSensitivities())
	}
	classes := SupportedGatewayDataClasses()
	if len(classes) == 0 || !containsString(classes, string(DataProviderCredentials)) || !containsString(classes, string(DataRequestBody)) {
		t.Fatalf("gateway data classes = %+v", classes)
	}
}
