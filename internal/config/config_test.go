package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

func TestLoadAcceptsCompleteServeConfiguration(t *testing.T) {
	temporary := t.TempDir()
	dataDir := filepath.Join(temporary, "data")
	docRoot := filepath.Join(temporary, "frontend")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(docRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(temporary, "mia.toml")
	content := "[main]\ndata_dir = \"" + dataDir + "\"\ndoc_root = \"" + docRoot + "\"\npublic_url = \"https://mia.example.test\"\n[openai]\napi_key = \"key\"\n[mistral]\napi_key = \"key\"\n[smtp]\nhost = \"smtp.example.test\"\nsender_email = \"mia@example.test\"\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	AddFlags(flags)
	if err := flags.Set("config", configPath); err != nil {
		t.Fatal(err)
	}
	configuration, err := Load(flags, true)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.HTTP.Listen != "127.0.0.1:9900" {
		t.Fatalf("listen = %q", configuration.HTTP.Listen)
	}
	if configuration.ClickSend.BaseURL != "https://rest.clicksend.com/v3" {
		t.Fatalf("ClickSend base URL = %q", configuration.ClickSend.BaseURL)
	}
}

func TestLoadRejectsUnknownConfigurationKey(t *testing.T) {
	temporary := t.TempDir()
	configPath := filepath.Join(temporary, "mia.toml")
	if err := os.WriteFile(configPath, []byte("[unknown]\nvalue = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	AddFlags(flags)
	if err := flags.Set("config", configPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(flags, false); err == nil {
		t.Fatal("Load accepted an unknown key")
	}
}

func TestLoadOfflineRequiresConfiguredDataDirectory(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	AddFlags(flags)
	_, err := Load(flags, false)
	if err == nil || !strings.Contains(err.Error(), "main.data_dir must be configured") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestExampleConfigurationDocumentsEverySchemaKey(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "mia.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	documented := documentedExampleKeys(string(content))
	schema := make(map[string]struct{}, len(settings))
	for _, setting := range settings {
		schema[setting.key] = struct{}{}
		if _, ok := documented[setting.key]; !ok {
			t.Errorf("mia.example.toml does not document %q", setting.key)
		}
	}
	for key := range documented {
		if _, ok := schema[key]; !ok {
			t.Errorf("mia.example.toml documents unknown key %q", key)
		}
	}
}

func documentedExampleKeys(content string) map[string]struct{} {
	keys := make(map[string]struct{})
	table := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			table = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			continue
		}
		if table == "" || !strings.Contains(line, "=") {
			continue
		}
		key := strings.TrimSpace(strings.SplitN(line, "=", 2)[0])
		if key != "" && !strings.ContainsAny(key, " \t") {
			keys[table+"."+key] = struct{}{}
		}
	}
	return keys
}

func TestValidateConfigFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mia.toml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []os.FileMode{0o600, 0o640} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := validateConfigFile(path); err != nil {
			t.Errorf("mode %04o rejected: %v", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o660, 0o650, 0o604} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := validateConfigFile(path); err == nil {
			t.Errorf("mode %04o accepted", mode)
		}
	}
}

func TestLoadNormalizesTrailingPublicURLSlash(t *testing.T) {
	configuration := Config{}
	configuration.Main.PublicURL = "https://mia.example.test/"
	if err := normalizePublicURL(&configuration); err != nil {
		t.Fatal(err)
	}
	if configuration.Main.PublicURL != "https://mia.example.test" {
		t.Fatalf("public URL = %q", configuration.Main.PublicURL)
	}
}

func TestNormalizePublicURLRestrictsHTTPToLoopback(t *testing.T) {
	for _, value := range []string{"http://localhost:9900", "http://127.0.0.1:9900", "http://[::1]:9900", "http://localhost"} {
		configuration := Config{}
		configuration.Main.PublicURL = value
		if err := normalizePublicURL(&configuration); err != nil {
			t.Errorf("%s rejected: %v", value, err)
		}
	}
	for _, value := range []string{"http://mia.example.test", "http://localhost.example.test", "http://10.0.0.1:9900", "ftp://localhost"} {
		configuration := Config{}
		configuration.Main.PublicURL = value
		if err := normalizePublicURL(&configuration); err == nil {
			t.Errorf("%s accepted", value)
		}
	}
}

func TestSupportedModelRegistryRejectsUnknownValues(t *testing.T) {
	if !supportedModel("gpt-5.6-terra") {
		t.Fatal("default model is unsupported")
	}
	if supportedModel("unknown-model") || supportedModel("") {
		t.Fatal("unsupported model accepted")
	}
}

func TestNormalizeTrustedProxyCIDRsTrimsAndRejectsEmptyValues(t *testing.T) {
	configuration := Config{}
	configuration.HTTP.TrustedProxyCIDRs = []string{"10.0.0.0/8, 192.168.0.0/16"}
	if err := normalizeListSettings(&configuration); err != nil {
		t.Fatal(err)
	}
	if strings.Join(configuration.HTTP.TrustedProxyCIDRs, ",") != "10.0.0.0/8,192.168.0.0/16" {
		t.Fatalf("normalized CIDRs = %v", configuration.HTTP.TrustedProxyCIDRs)
	}
	configuration.HTTP.TrustedProxyCIDRs = []string{"10.0.0.0/8,,192.168.0.0/16"}
	if err := normalizeListSettings(&configuration); err == nil {
		t.Fatal("empty CIDR accepted")
	}
}

func TestLoadNormalizesTrustedProxyCIDRsFromEnvironmentAndFlags(t *testing.T) {
	for name, configure := range map[string]func(*pflag.FlagSet){
		"environment": func(_ *pflag.FlagSet) { t.Setenv("MIA_HTTP_TRUSTED_PROXY_CIDRS", "10.0.0.0/8, 192.168.0.0/16") },
		"flag": func(flags *pflag.FlagSet) {
			if err := flags.Set("http-trusted-proxy-cidrs", "10.0.0.0/8, 192.168.0.0/16"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			temporary := t.TempDir()
			dataDir := filepath.Join(temporary, "data")
			docRoot := filepath.Join(temporary, "frontend")
			if err := os.Mkdir(dataDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(docRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(temporary, "mia.toml")
			content := "[main]\ndata_dir = \"" + dataDir + "\"\ndoc_root = \"" + docRoot + "\"\npublic_url = \"https://mia.example.test\"\n[openai]\napi_key = \"key\"\n[mistral]\napi_key = \"key\"\n[smtp]\nhost = \"smtp.example.test\"\nsender_email = \"mia@example.test\"\n"
			if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			AddFlags(flags)
			if err := flags.Set("config", configPath); err != nil {
				t.Fatal(err)
			}
			configure(flags)
			configuration, err := Load(flags, true)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(configuration.HTTP.TrustedProxyCIDRs, ",") != "10.0.0.0/8,192.168.0.0/16" {
				t.Fatalf("CIDRs = %v", configuration.HTTP.TrustedProxyCIDRs)
			}
		})
	}
}

func TestLoadOverridesClickSendBaseURLFromEnvironmentAndFlag(t *testing.T) {
	for name, configure := range map[string]func(*pflag.FlagSet){
		"environment": func(_ *pflag.FlagSet) {
			t.Setenv("MIA_CLICKSEND_BASE_URL", "http://127.0.0.1:3550/")
		},
		"flag": func(flags *pflag.FlagSet) {
			if err := flags.Set("clicksend-base-url", "http://localhost:3550"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			temporary := t.TempDir()
			dataDir := filepath.Join(temporary, "data")
			docRoot := filepath.Join(temporary, "frontend")
			if err := os.Mkdir(dataDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(docRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(temporary, "mia.toml")
			content := "[main]\ndata_dir = \"" + dataDir + "\"\ndoc_root = \"" + docRoot +
				"\"\npublic_url = \"https://mia.example.test\"\n[openai]\napi_key = \"key\"\n" +
				"[mistral]\napi_key = \"key\"\n[smtp]\nhost = \"smtp.example.test\"\n" +
				"sender_email = \"mia@example.test\"\n[clicksend]\nusername = \"account\"\n" +
				"api_key = \"key\"\nbase_url = \"https://toml.example.test\"\n"
			if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			AddFlags(flags)
			if err := flags.Set("config", configPath); err != nil {
				t.Fatal(err)
			}
			configure(flags)
			configuration, err := Load(flags, true)
			if err != nil {
				t.Fatal(err)
			}
			want := "http://127.0.0.1:3550"
			if name == "flag" {
				want = "http://localhost:3550"
			}
			if configuration.ClickSend.BaseURL != want {
				t.Fatalf("ClickSend base URL = %q, want %q", configuration.ClickSend.BaseURL, want)
			}
		})
	}
}

func TestLoadRejectsInvalidClickSendBaseURLFromEverySource(t *testing.T) {
	for name, configure := range map[string]func(*pflag.FlagSet, string){
		"TOML": func(_ *pflag.FlagSet, configPath string) {
			content, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			content = append(content, []byte("[clicksend]\nbase_url = \"https://clicksend.example.test:65536\"\n")...)
			if err := os.WriteFile(configPath, content, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"environment": func(_ *pflag.FlagSet, _ string) {
			t.Setenv("MIA_CLICKSEND_BASE_URL", "https://clicksend.example.test:0")
		},
		"flag": func(flags *pflag.FlagSet, _ string) {
			if err := flags.Set("clicksend-base-url", "https://clicksend.example.test:65536"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			temporary := t.TempDir()
			dataDir := filepath.Join(temporary, "data")
			docRoot := filepath.Join(temporary, "frontend")
			if err := os.Mkdir(dataDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(docRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(temporary, "mia.toml")
			content := "[main]\ndata_dir = \"" + dataDir + "\"\ndoc_root = \"" + docRoot +
				"\"\npublic_url = \"https://mia.example.test\"\n[openai]\napi_key = \"key\"\n" +
				"[mistral]\napi_key = \"key\"\n[smtp]\nhost = \"smtp.example.test\"\n" +
				"sender_email = \"mia@example.test\"\n"
			if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			AddFlags(flags)
			if err := flags.Set("config", configPath); err != nil {
				t.Fatal(err)
			}
			configure(flags, configPath)
			if _, err := Load(flags, true); err == nil || !strings.Contains(err.Error(), "clicksend.base_url") {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoadOfflineIgnoresInvalidClickSendBaseURL(t *testing.T) {
	temporary := t.TempDir()
	dataDir := filepath.Join(temporary, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(temporary, "mia.toml")
	content := "[main]\ndata_dir = \"" + dataDir + "\"\n[clicksend]\nbase_url = \"https://clicksend.example.test:65536\"\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	AddFlags(flags)
	if err := flags.Set("config", configPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(flags, false); err != nil {
		t.Fatalf("offline Load error = %v", err)
	}
}

func TestNormalizeClickSendBaseURL(t *testing.T) {
	for name, test := range map[string]struct {
		value string
		want  string
		valid bool
	}{
		"default HTTPS":      {"https://rest.clicksend.com/v3", "https://rest.clicksend.com/v3", true},
		"HTTPS root slash":   {"https://clicksend.example.test/", "https://clicksend.example.test", true},
		"HTTPS v3 slash":     {"https://clicksend.example.test/v3/", "https://clicksend.example.test/v3", true},
		"localhost HTTP":     {"http://localhost:3550", "http://localhost:3550", true},
		"loopback IPv4 HTTP": {"http://127.0.0.1:3550/", "http://127.0.0.1:3550", true},
		"loopback IPv6 HTTP": {"http://[::1]:3550", "http://[::1]:3550", true},
		"empty port":         {"https://clicksend.example.test:", "", false},
		"non-numeric port":   {"https://clicksend.example.test:http", "", false},
		"zero port":          {"https://clicksend.example.test:0", "", false},
		"out of range port":  {"https://clicksend.example.test:65536", "", false},
		"credentials":        {"https://user@clicksend.example.test", "", false},
		"query":              {"https://clicksend.example.test?next=x", "", false},
		"fragment":           {"https://clicksend.example.test#fragment", "", false},
		"other path":         {"https://clicksend.example.test/other", "", false},
		"non-loopback HTTP":  {"http://clicksend.example.test", "", false},
		"unsupported scheme": {"ftp://clicksend.example.test", "", false},
		"encoded path":       {"https://clicksend.example.test/%76%33", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			configuration := Config{}
			configuration.ClickSend.BaseURL = test.value
			err := normalizeClickSendBaseURL(&configuration)
			if test.valid {
				if err != nil {
					t.Fatal(err)
				}
				if configuration.ClickSend.BaseURL != test.want {
					t.Fatalf("base URL = %q, want %q", configuration.ClickSend.BaseURL, test.want)
				}
			} else if err == nil {
				t.Fatal("invalid base URL accepted")
			}
		})
	}
}

// Listing every missing setting at once, with the environment variable that
// overrides it, avoids a fix-one-restart-repeat cycle.
func TestRequiredSettingsNameEveryMissingKey(t *testing.T) {
	if err := requiredSettings(&Config{}); err != nil {
		for _, want := range []string{
			"openai.api_key", "MIA_OPENAI_API_KEY",
			"mistral.api_key", "MIA_MISTRAL_API_KEY",
			"smtp.host", "MIA_SMTP_HOST",
			"smtp.sender_email", "MIA_SMTP_SENDER_EMAIL",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}
	} else {
		t.Fatal("empty configuration accepted")
	}
	complete := Config{}
	complete.OpenAI.APIKey = "key"
	complete.Mistral.APIKey = "key"
	complete.SMTP.Host = "smtp.example.test"
	complete.SMTP.SenderEmail = "mia@example.test"
	if err := requiredSettings(&complete); err != nil {
		t.Fatalf("complete configuration rejected: %v", err)
	}
}

// An operator cannot act on "invalid upload limits", so each bound names its
// own setting and the range it accepts.
func TestUploadLimitsNameTheOffendingSetting(t *testing.T) {
	valid := func() Config {
		configuration := Config{}
		configuration.Uploads.MaxFileSizeMiB = 100
		configuration.Uploads.MaxMaterialSizeMiB = 200
		configuration.Uploads.MaxMaterialPages = 1000
		configuration.Uploads.MaxMaterialFiles = 200
		configuration.Uploads.MaxImageMegapixels = 40
		return configuration
	}
	if err := uploadLimits(&Config{Uploads: valid().Uploads}); err != nil {
		t.Fatalf("valid limits rejected: %v", err)
	}
	for name, testCase := range map[string]struct {
		mutate func(*Config)
		expect []string
	}{
		"file size too small":   {func(c *Config) { c.Uploads.MaxFileSizeMiB = 0 }, []string{"uploads.max_file_size_mib", "1", "512"}},
		"material size too big": {func(c *Config) { c.Uploads.MaxMaterialSizeMiB = 900 }, []string{"uploads.max_material_size_mib", "512"}},
		"pages out of range":    {func(c *Config) { c.Uploads.MaxMaterialPages = 5000 }, []string{"uploads.max_material_pages", "2000"}},
		"files out of range":    {func(c *Config) { c.Uploads.MaxMaterialFiles = 0 }, []string{"uploads.max_material_files", "200"}},
		"megapixels out of range": {func(c *Config) { c.Uploads.MaxImageMegapixels = 999 },
			[]string{"uploads.max_image_megapixels", "100"}},
		"material smaller than file": {func(c *Config) { c.Uploads.MaxMaterialSizeMiB = 50 },
			[]string{"uploads.max_material_size_mib", "uploads.max_file_size_mib"}},
	} {
		t.Run(name, func(t *testing.T) {
			configuration := valid()
			testCase.mutate(&configuration)
			err := uploadLimits(&configuration)
			if err == nil {
				t.Fatal("invalid limit accepted")
			}
			for _, want := range testCase.expect {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// The two model settings are validated separately so the message identifies
// which one the operator has to change.
func TestUnsupportedModelErrorNamesItsSetting(t *testing.T) {
	for setting, variable := range map[string]string{
		"openai.chat_model": "MIA_OPENAI_CHAT_MODEL",
		"openai.job_model":  "MIA_OPENAI_JOB_MODEL",
	} {
		t.Run(setting, func(t *testing.T) {
			temporary := t.TempDir()
			dataDir := filepath.Join(temporary, "data")
			docRoot := filepath.Join(temporary, "frontend")
			if err := os.Mkdir(dataDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(docRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(temporary, "mia.toml")
			content := "[main]\ndata_dir = \"" + dataDir + "\"\ndoc_root = \"" + docRoot +
				"\"\npublic_url = \"https://mia.example.test\"\n[openai]\napi_key = \"key\"\n[mistral]\napi_key = \"key\"\n" +
				"[smtp]\nhost = \"smtp.example.test\"\nsender_email = \"mia@example.test\"\n"
			if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(variable, "unsupported-model")
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			AddFlags(flags)
			if err := flags.Set("config", configPath); err != nil {
				t.Fatal(err)
			}
			_, err := Load(flags, true)
			if err == nil {
				t.Fatal("unsupported model accepted")
			}
			if !strings.Contains(err.Error(), setting) || !strings.Contains(err.Error(), "unsupported-model") {
				t.Fatalf("error %q does not name %q and its value", err, setting)
			}
		})
	}
}
