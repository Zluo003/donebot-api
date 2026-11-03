package adminaccess

import "testing"

func TestScopedPermissionCatalogRequiresExplicitGrant(t *testing.T) {
	for _, id := range []string{"accounts.identity.read", "accounts.usage.read", "accounts.options.write", "accounts.rate.write", "groups.list.read", "groups.capacity.read", "openai.tokens.refresh"} {
		found := false
		for _, entry := range PermissionCatalog() {
			if entry.ID == id {
				found = true
			}
		}
		if !found {
			t.Errorf("权限未登记: %s", id)
		}
	}
	if RouteAllowed(Policy{}, "POST", "/api/v1/admin/openai/refresh-token") {
		t.Fatal("旧策略不得继承新增权限")
	}
}

func TestDoneBotInstallRoutesRequireExplicitGlobalPermissions(t *testing.T) {
	for _, tc := range []struct{ method, path, permission string }{
		{"GET", "/api/v1/admin/donebot/installs", "donebot.installs.read"},
		{"DELETE", "/api/v1/admin/donebot/installs/:id", "donebot.installs.revoke"},
	} {
		if RouteAllowed(Policy{}, tc.method, tc.path) {
			t.Fatalf("%s %s must require a permission", tc.method, tc.path)
		}
		if !RouteAllowed(Policy{GlobalPermissions: []string{tc.permission}}, tc.method, tc.path) {
			t.Errorf("%s did not grant %s", tc.permission, tc.path)
		}
		found := false
		for _, permission := range PermissionCatalog() {
			if permission.ID == tc.permission {
				found = true
			}
		}
		if !found {
			t.Errorf("permission %q is missing from the catalog", tc.permission)
		}
	}
}
