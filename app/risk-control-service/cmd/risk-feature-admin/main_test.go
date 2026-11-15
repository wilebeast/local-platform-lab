package main

import "testing"

func TestLoadAdminConfigRequiresHashedBearerCredentials(t *testing.T) {
	values := map[string]string{
		"POSTGRES_DSN": "postgres://example",
		"FEATURE_ADMIN_CREDENTIALS_JSON": `[{"subject":"operator","token_sha256":"` +
			"87bb9d5b8a2897ca76e6f4e5f78e353824eec710bd6d122c487b4748ea60f2e2" +
			`","roles":["feature_operator"]}]`,
	}
	config, err := loadAdminConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.listenAddress != ":8081" || len(config.credentials) != 1 ||
		config.credentials[0].Subject != "operator" {
		t.Fatalf("config = %+v", config)
	}

	values["FEATURE_ADMIN_CREDENTIALS_JSON"] = `[{"subject":"operator","token":"plaintext"}]`
	if _, err := loadAdminConfig(func(key string) string { return values[key] }); err == nil {
		t.Fatal("expected plaintext or incomplete credential to be rejected")
	}
}
